package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// HAClient is the outbound connection to Home Assistant's WebSocket API.
// It feeds the bar panel: entity states, live updates and service calls.
type HAClient struct {
	cfg *ConfigStore

	mu       sync.RWMutex
	phase    string // unconfigured | connecting | connected | error
	lastErr  string
	entities map[string]map[string]any
	haName   string
	conn     *websocket.Conn
	writeMu  sync.Mutex
	nextID   atomic.Int64
	pending  map[int64]chan haResult
	reconfig chan struct{}

	OnPhase   func()
	OnState   func(entity map[string]any)
	OnRemoved func(entityID string)
	OnSnapshot func()
}

type haResult struct {
	ok  bool
	err string
}

var errAuth = errors.New("authentication failed: check the access token")

func NewHAClient(cfg *ConfigStore) *HAClient {
	return &HAClient{
		cfg:      cfg,
		phase:    "unconfigured",
		entities: map[string]map[string]any{},
		pending:  map[int64]chan haResult{},
		reconfig: make(chan struct{}, 1),
	}
}

func (h *HAClient) Reconfigure() {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "reconfigure")
	}
	select {
	case h.reconfig <- struct{}{}:
	default:
	}
}

func (h *HAClient) Status() (phase, lastErr, name string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.phase, h.lastErr, h.haName
}

func (h *HAClient) setPhase(phase, err string) {
	h.mu.Lock()
	changed := h.phase != phase || h.lastErr != err
	h.phase, h.lastErr = phase, err
	h.mu.Unlock()
	if changed && h.OnPhase != nil {
		h.OnPhase()
	}
}

func (h *HAClient) Entity(id string) map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.entities[id]
}

// List returns every entity, sorted by domain then name, for the device
// picker.
func (h *HAClient) List() []map[string]any {
	h.mu.RLock()
	out := make([]map[string]any, 0, len(h.entities))
	for _, e := range h.entities {
		out = append(out, e)
	}
	h.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		di, dj := out[i]["domain"].(string), out[j]["domain"].(string)
		if di != dj {
			return di < dj
		}
		return strings.ToLower(out[i]["name"].(string)) < strings.ToLower(out[j]["name"].(string))
	})
	return out
}

func wsURL(base string) (string, error) {
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://") + "/api/websocket", nil
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://") + "/api/websocket", nil
	}
	return "", fmt.Errorf("URL must start with http:// or https://")
}

func (h *HAClient) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		c := h.cfg.Get()
		if c.HAURL == "" || c.HAToken == "" {
			h.setPhase("unconfigured", "")
			h.wait(ctx, 0)
			continue
		}
		h.setPhase("connecting", "")
		start := time.Now()
		err := h.session(ctx, c.HAURL, c.HAToken)
		if ctx.Err() != nil {
			return
		}
		h.failPending()
		if errors.Is(err, errAuth) {
			h.setPhase("error", err.Error())
			h.wait(ctx, 0) // wait for a new token; retrying won't help
			backoff = time.Second
			continue
		}
		msg := "connection lost"
		if err != nil {
			msg = err.Error()
		}
		h.setPhase("error", msg)
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		log.Printf("ha: %s; retrying in %s", msg, backoff)
		if h.wait(ctx, backoff) {
			backoff = time.Second
		} else if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// wait blocks until reconfigured (returns true), timed out, or cancelled.
// d == 0 waits only for reconfiguration.
func (h *HAClient) wait(ctx context.Context, d time.Duration) bool {
	var timer <-chan time.Time
	if d > 0 {
		timer = time.After(d)
	}
	select {
	case <-ctx.Done():
	case <-h.reconfig:
		return true
	case <-timer:
	}
	return false
}

func (h *HAClient) failPending() {
	h.mu.Lock()
	for id, ch := range h.pending {
		ch <- haResult{ok: false, err: "disconnected"}
		delete(h.pending, id)
	}
	h.mu.Unlock()
}

func (h *HAClient) write(ctx context.Context, conn *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, b)
}

func (h *HAClient) read(ctx context.Context, conn *websocket.Conn) (map[string]any, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (h *HAClient) session(ctx context.Context, base, token string) error {
	u, err := wsURL(base)
	if err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dctx, u, nil)
	cancel()
	if err != nil {
		return fmt.Errorf("cannot reach Home Assistant: %v", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 20) // get_states on a big install is several MB

	actx, acancel := context.WithTimeout(ctx, 15*time.Second)
	defer acancel()
	msg, err := h.read(actx, conn)
	if err != nil || msg["type"] != "auth_required" {
		return fmt.Errorf("unexpected handshake from Home Assistant")
	}
	if err := h.write(actx, conn, map[string]any{"type": "auth", "access_token": token}); err != nil {
		return err
	}
	msg, err = h.read(actx, conn)
	if err != nil {
		return err
	}
	if msg["type"] == "auth_invalid" {
		return errAuth
	}
	if msg["type"] != "auth_ok" {
		return fmt.Errorf("unexpected auth reply %v", msg["type"])
	}
	acancel()

	h.nextID.Store(10)
	if err := h.write(ctx, conn, map[string]any{"id": 1, "type": "subscribe_events", "event_type": "state_changed"}); err != nil {
		return err
	}
	if err := h.write(ctx, conn, map[string]any{"id": 2, "type": "get_states"}); err != nil {
		return err
	}
	if err := h.write(ctx, conn, map[string]any{"id": 3, "type": "get_config"}); err != nil {
		return err
	}

	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.conn = nil
		h.mu.Unlock()
	}()

	// HA closes idle clients that don't answer; keep it chatty.
	sctx, scancel := context.WithCancel(ctx)
	defer scancel()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-t.C:
				id := h.nextID.Add(1)
				if h.write(sctx, conn, map[string]any{"id": id, "type": "ping"}) != nil {
					conn.CloseNow()
					return
				}
			}
		}
	}()

	for {
		msg, err := h.read(ctx, conn)
		if err != nil {
			return err
		}
		h.handle(msg)
	}
}

func (h *HAClient) handle(msg map[string]any) {
	id, _ := msg["id"].(float64)
	switch msg["type"] {
	case "result":
		success, _ := msg["success"].(bool)
		switch int64(id) {
		case 2:
			list, _ := msg["result"].([]any)
			ents := make(map[string]map[string]any, len(list))
			for _, raw := range list {
				if m, ok := raw.(map[string]any); ok {
					if e := trimEntity(m); e != nil {
						ents[e["entity_id"].(string)] = e
					}
				}
			}
			h.mu.Lock()
			h.entities = ents
			h.mu.Unlock()
			h.setPhase("connected", "")
			if h.OnSnapshot != nil {
				h.OnSnapshot()
			}
			return
		case 3:
			if r, ok := msg["result"].(map[string]any); ok {
				name, _ := r["location_name"].(string)
				h.mu.Lock()
				h.haName = name
				h.mu.Unlock()
				if h.OnPhase != nil {
					h.OnPhase()
				}
			}
			return
		}
		h.mu.Lock()
		ch, ok := h.pending[int64(id)]
		delete(h.pending, int64(id))
		h.mu.Unlock()
		if ok {
			res := haResult{ok: success}
			if e, ok := msg["error"].(map[string]any); ok {
				res.err, _ = e["message"].(string)
			}
			ch <- res
		}
	case "event":
		ev, _ := msg["event"].(map[string]any)
		data, _ := ev["data"].(map[string]any)
		entityID, _ := data["entity_id"].(string)
		if entityID == "" {
			return
		}
		ns, _ := data["new_state"].(map[string]any)
		if ns == nil {
			h.mu.Lock()
			delete(h.entities, entityID)
			h.mu.Unlock()
			if h.OnRemoved != nil {
				h.OnRemoved(entityID)
			}
			return
		}
		e := trimEntity(ns)
		if e == nil {
			return
		}
		h.mu.Lock()
		h.entities[entityID] = e
		h.mu.Unlock()
		if h.OnState != nil {
			h.OnState(e)
		}
	}
}

// CallService runs a Home Assistant service and waits for its result.
func (h *HAClient) CallService(ctx context.Context, domain, service, entityID string, data map[string]any) error {
	h.mu.RLock()
	conn := h.conn
	h.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("not connected to Home Assistant")
	}
	id := h.nextID.Add(1)
	ch := make(chan haResult, 1)
	h.mu.Lock()
	h.pending[id] = ch
	h.mu.Unlock()
	msg := map[string]any{"id": id, "type": "call_service", "domain": domain, "service": service}
	if data == nil {
		data = map[string]any{}
	}
	if entityID != "" {
		msg["target"] = map[string]any{"entity_id": entityID}
	}
	msg["service_data"] = data
	if err := h.write(ctx, conn, msg); err != nil {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return err
	}
	select {
	case r := <-ch:
		if !r.ok {
			if r.err == "" {
				r.err = "service call failed"
			}
			return errors.New(r.err)
		}
		return nil
	case <-time.After(15 * time.Second):
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
		return fmt.Errorf("Home Assistant did not answer")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Attributes the panel actually uses; everything else stays out of the
// QML side to keep it light.
var keepAttrs = []string{
	"unit_of_measurement", "device_class", "icon", "supported_features",
	"brightness", "color_mode", "supported_color_modes", "hs_color", "rgb_color",
	"color_temp_kelvin", "min_color_temp_kelvin", "max_color_temp_kelvin",
	"current_temperature", "temperature", "target_temp_low", "target_temp_high",
	"hvac_modes", "hvac_action", "min_temp", "max_temp", "target_temp_step",
	"current_position", "percentage",
	"media_title", "media_artist", "volume_level", "is_volume_muted", "entity_picture",
	"min", "max", "step", "options",
}

func trimEntity(raw map[string]any) map[string]any {
	id, _ := raw["entity_id"].(string)
	if id == "" {
		return nil
	}
	domain, _, _ := strings.Cut(id, ".")
	attrs, _ := raw["attributes"].(map[string]any)
	name, _ := attrs["friendly_name"].(string)
	if name == "" {
		name = id
	}
	out := map[string]any{
		"entity_id": id,
		"domain":    domain,
		"state":     raw["state"],
		"name":      name,
	}
	a := map[string]any{}
	for _, k := range keepAttrs {
		if v, ok := attrs[k]; ok {
			a[k] = v
		}
	}
	out["attributes"] = a
	return out
}
