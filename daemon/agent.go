package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const agentProtocol = 1

// AgentServer is what the Home Assistant integration connects to. One
// WebSocket per config entry; the PC pushes state, HA sends commands.
type AgentServer struct {
	cfg     *ConfigStore
	pc      *PC
	media   *Media
	mu      sync.Mutex
	clients map[*agentClient]struct{}
	onCount func(int)
}

type agentClient struct {
	send chan []byte
	addr string
}

func NewAgentServer(cfg *ConfigStore, pc *PC, media *Media) *AgentServer {
	return &AgentServer{cfg: cfg, pc: pc, media: media, clients: map[*agentClient]struct{}{}}
}

func (a *AgentServer) Clients() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []string{}
	for c := range a.clients {
		out = append(out, c.addr)
	}
	return out
}

func (a *AgentServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", a.serveWS)
	mux.HandleFunc("/art", a.serveArt)
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		c := a.cfg.Get()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"device_id": c.DeviceID, "name": c.DeviceName, "version": version, "protocol": agentProtocol})
	})
	return mux
}

func (a *AgentServer) Serve(ctx context.Context) error {
	port := a.cfg.Get().AgentPort
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	log.Printf("agent: listening on :%d", port)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (a *AgentServer) keyOK(key string) bool {
	want := a.cfg.Get().AgentKey
	return subtle.ConstantTimeCompare([]byte(key), []byte(want)) == 1
}

func deviceInfo(c Config) map[string]any {
	vendor := readTrim("/sys/class/dmi/id/sys_vendor")
	model := readTrim("/sys/class/dmi/id/product_version")
	if model == "" || strings.EqualFold(model, "none") || strings.Contains(strings.ToLower(model), "to be filled") {
		model = readTrim("/sys/class/dmi/id/product_name")
	}
	if model == "" {
		model = "Linux PC"
	}
	return map[string]any{
		"id":           c.DeviceID,
		"name":         c.DeviceName,
		"manufacturer": vendor,
		"model":        model,
		"sw_version":   version,
		"hostname":     hostname(),
	}
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func (a *AgentServer) snapshot(kind string) []byte {
	c := a.cfg.Get()
	ents := a.pc.Exposed(c.Expose)
	keys := make([]string, len(ents))
	for i, e := range ents {
		keys[i] = e.Key
	}
	msg := map[string]any{
		"type":     kind,
		"version":  agentProtocol,
		"device":   deviceInfo(c),
		"entities": ents,
		"states":   a.pc.States(keys),
	}
	b, _ := json.Marshal(msg)
	return b
}

func (a *AgentServer) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer conn.CloseNow()

	// Handshake: the first message must carry the pairing key.
	hctx, hcancel := context.WithTimeout(ctx, 10*time.Second)
	_, data, err := conn.Read(hctx)
	hcancel()
	if err != nil {
		return
	}
	var hello struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if json.Unmarshal(data, &hello) != nil || hello.Type != "hello" || !a.keyOK(hello.Key) {
		b, _ := json.Marshal(map[string]any{"type": "error", "error": "invalid_key"})
		conn.Write(ctx, websocket.MessageText, b)
		conn.Close(websocket.StatusPolicyViolation, "invalid key")
		return
	}

	client := &agentClient{send: make(chan []byte, 256), addr: r.RemoteAddr}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		client.addr = host
	}
	if err := conn.Write(ctx, websocket.MessageText, a.snapshot("welcome")); err != nil {
		return
	}
	a.mu.Lock()
	a.clients[client] = struct{}{}
	n := len(a.clients)
	a.mu.Unlock()
	log.Printf("agent: home assistant connected from %s", client.addr)
	if a.onCount != nil {
		a.onCount(n)
	}
	defer func() {
		a.mu.Lock()
		delete(a.clients, client)
		n := len(a.clients)
		a.mu.Unlock()
		log.Printf("agent: home assistant at %s disconnected", client.addr)
		if a.onCount != nil {
			a.onCount(n)
		}
	}()

	go func() {
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-client.send:
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(wctx, websocket.MessageText, b)
				wcancel()
				if err != nil {
					cancel()
					return
				}
			case <-ping.C:
				pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			Type  string `json:"type"`
			ID    int64  `json:"id"`
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "command":
			go func() {
				err := a.runCommand(msg.Key, msg.Value)
				res := map[string]any{"type": "result", "id": msg.ID, "ok": err == nil}
				if err != nil {
					res["error"] = err.Error()
					log.Printf("agent: command %s failed: %v", msg.Key, err)
				}
				b, _ := json.Marshal(res)
				a.trySend(client, b)
			}()
		case "ping":
			b, _ := json.Marshal(map[string]any{"type": "pong", "id": msg.ID})
			a.trySend(client, b)
		}
	}
}

func (a *AgentServer) runCommand(key string, value any) error {
	exposed := false
	for _, e := range a.pc.Exposed(a.cfg.Get().Expose) {
		if e.Key == key {
			exposed = true
			break
		}
	}
	if !exposed {
		return fmt.Errorf("%s is not enabled on the PC", key)
	}
	return a.pc.Run(key, value)
}

func (a *AgentServer) trySend(c *agentClient, b []byte) {
	select {
	case c.send <- b:
	default: // slow client; it'll resync on reconnect
	}
}

func (a *AgentServer) broadcast(b []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for c := range a.clients {
		a.trySend(c, b)
	}
}

// StatesChanged forwards changed values for exposed entities.
func (a *AgentServer) StatesChanged(changes map[string]any) {
	expose := a.cfg.Get().Expose
	exposed := map[string]bool{}
	for _, e := range a.pc.Exposed(expose) {
		exposed[e.Key] = true
	}
	out := map[string]any{}
	for k, v := range changes {
		if exposed[k] {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return
	}
	b, _ := json.Marshal(map[string]any{"type": "states", "states": out})
	a.broadcast(b)
}

// SchemaChanged resends the full entity list (expose toggles, renamed
// device, new custom commands). The integration reloads its entities.
func (a *AgentServer) SchemaChanged() {
	a.broadcast(a.snapshot("entities"))
}

func (a *AgentServer) Event(event string, data map[string]any) {
	b, _ := json.Marshal(map[string]any{"type": "event", "event": event, "data": data})
	a.broadcast(b)
}

// serveArt hands Home Assistant the current track artwork, which for most
// Linux players is a local file it couldn't otherwise reach.
func (a *AgentServer) serveArt(w http.ResponseWriter, r *http.Request) {
	if !a.keyOK(r.URL.Query().Get("key")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	art := a.media.ArtURL()
	u, err := url.Parse(art)
	if art == "" || err != nil {
		http.NotFound(w, r)
		return
	}
	switch u.Scheme {
	case "file":
		path := u.Path
		ctype := mime.TypeByExtension(filepath.Ext(path))
		if ctype == "" {
			ctype = "image/jpeg"
		}
		f, err := os.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", ctype)
		io.Copy(w, io.LimitReader(f, 10<<20))
	case "http", "https":
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(art)
		if err != nil {
			http.Error(w, "fetch failed", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, io.LimitReader(resp.Body, 10<<20))
	default:
		http.NotFound(w, r)
	}
}
