package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testConfig(t *testing.T) *ConfigStore {
	t.Helper()
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// fakeHA is just enough of Home Assistant's WebSocket API.
type fakeHA struct {
	mu    sync.Mutex
	calls []map[string]any
	conn  *websocket.Conn
	token string
}

func (f *fakeHA) handler(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx := r.Context()
	send := func(v any) { b, _ := json.Marshal(v); conn.Write(ctx, websocket.MessageText, b) }
	recv := func() map[string]any {
		_, b, err := conn.Read(ctx)
		if err != nil {
			return nil
		}
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}
	send(map[string]any{"type": "auth_required"})
	auth := recv()
	if auth["access_token"] != f.token {
		send(map[string]any{"type": "auth_invalid"})
		conn.Close(websocket.StatusNormalClosure, "")
		return
	}
	send(map[string]any{"type": "auth_ok"})
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	for {
		m := recv()
		if m == nil {
			return
		}
		id := m["id"]
		switch m["type"] {
		case "get_states":
			send(map[string]any{"id": id, "type": "result", "success": true, "result": []any{
				map[string]any{"entity_id": "light.desk", "state": "off", "attributes": map[string]any{"friendly_name": "Desk", "brightness": nil, "secret": "x"}},
				map[string]any{"entity_id": "sensor.temp", "state": "21.5", "attributes": map[string]any{"friendly_name": "Temp", "unit_of_measurement": "°C"}},
			}})
		case "get_config":
			send(map[string]any{"id": id, "type": "result", "success": true, "result": map[string]any{"location_name": "Home"}})
		case "call_service":
			f.mu.Lock()
			f.calls = append(f.calls, m)
			f.mu.Unlock()
			send(map[string]any{"id": id, "type": "result", "success": true, "result": nil})
			send(map[string]any{"id": 1, "type": "event", "event": map[string]any{"data": map[string]any{
				"entity_id": "light.desk",
				"new_state": map[string]any{"entity_id": "light.desk", "state": "on", "attributes": map[string]any{"friendly_name": "Desk", "brightness": 200}},
			}}})
		default:
			send(map[string]any{"id": id, "type": "result", "success": true, "result": nil})
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHAClient(t *testing.T) {
	fake := &fakeHA{token: "good"}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer srv.Close()

	cfg := testConfig(t)
	cfg.Update(func(c *Config) { c.HAURL = srv.URL + "/"; c.HAToken = "good" })
	if got := cfg.Get().HAURL; strings.HasSuffix(got, "/") {
		t.Fatalf("trailing slash kept: %q", got)
	}

	ha := NewHAClient(cfg)
	var mu sync.Mutex
	var updates []map[string]any
	ha.OnState = func(e map[string]any) { mu.Lock(); updates = append(updates, e); mu.Unlock() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ha.Run(ctx)

	waitFor(t, "connected", func() bool { p, _, _ := ha.Status(); return p == "connected" })
	if e := ha.Entity("light.desk"); e == nil || e["name"] != "Desk" {
		t.Fatalf("bad entity: %v", e)
	}
	if _, leaked := ha.Entity("light.desk")["attributes"].(map[string]any)["secret"]; leaked {
		t.Fatal("unlisted attribute was forwarded")
	}
	waitFor(t, "location name", func() bool { _, _, n := ha.Status(); return n == "Home" })

	if err := ha.CallService(ctx, "light", "turn_on", "light.desk", map[string]any{"brightness_pct": 80}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "state update", func() bool { mu.Lock(); defer mu.Unlock(); return len(updates) == 1 })
	if ha.Entity("light.desk")["state"] != "on" {
		t.Fatal("state not updated")
	}
	fake.mu.Lock()
	call := fake.calls[0]
	fake.mu.Unlock()
	if call["domain"] != "light" || call["target"].(map[string]any)["entity_id"] != "light.desk" {
		t.Fatalf("bad call: %v", call)
	}

	// A wrong token parks the client in an error state without hammering.
	cfg.Update(func(c *Config) { c.HAToken = "bad" })
	ha.Reconfigure()
	waitFor(t, "auth error", func() bool { p, e, _ := ha.Status(); return p == "error" && strings.Contains(e, "token") })
}

func TestAgentProtocol(t *testing.T) {
	cfg := testConfig(t)
	pc := NewPC()
	var ran []any
	pc.Define(PCEntity{Key: "cpu", Platform: "sensor", Name: "CPU", DefaultOn: true}, nil)
	pc.Define(PCEntity{Key: "hidden", Platform: "sensor", Name: "Hidden"}, nil)
	pc.Define(PCEntity{Key: "vol", Platform: "number", Name: "Vol", DefaultOn: true}, func(v any) error { ran = append(ran, v); return nil })
	pc.Define(PCEntity{Key: "reboot", Platform: "button", Name: "Reboot"}, func(any) error { t.Fatal("disabled command ran"); return nil })
	pc.Set("cpu", 12.3456)

	agent := NewAgentServer(cfg, pc, &Media{})
	pc.onChange = agent.StatesChanged
	srv := httptest.NewServer(agent.Handler())
	defer srv.Close()
	ctx := context.Background()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// Wrong key is refused.
	bad, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	bad.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","key":"nope"}`))
	_, b, _ := bad.Read(ctx)
	if !strings.Contains(string(b), "invalid_key") {
		t.Fatalf("expected invalid_key, got %s", b)
	}

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	hello, _ := json.Marshal(map[string]any{"type": "hello", "key": cfg.Get().AgentKey})
	conn.Write(ctx, websocket.MessageText, hello)
	read := func() map[string]any {
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, b, err := conn.Read(rctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}
	welcome := read()
	if welcome["type"] != "welcome" {
		t.Fatalf("got %v", welcome)
	}
	ents := welcome["entities"].([]any)
	if len(ents) != 2 {
		t.Fatalf("expected 2 exposed entities, got %d", len(ents))
	}
	if welcome["states"].(map[string]any)["cpu"] != 12.35 {
		t.Fatalf("bad cpu state %v", welcome["states"])
	}

	pc.Set("hidden", 1.0) // not exposed: must not be sent
	pc.Set("cpu", 50.0)
	st := read()
	if st["type"] != "states" || st["states"].(map[string]any)["cpu"] != 50.0 || st["states"].(map[string]any)["hidden"] != nil {
		t.Fatalf("bad states push %v", st)
	}

	conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","id":7,"key":"vol","value":40}`))
	res := read()
	if res["ok"] != true || res["id"] != 7.0 || len(ran) != 1 {
		t.Fatalf("command failed: %v", res)
	}
	conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","id":8,"key":"reboot"}`))
	res = read()
	if res["ok"] != false {
		t.Fatalf("disabled command should fail: %v", res)
	}
}

func TestParseWpctl(t *testing.T) {
	v, muted, ok := parseWpctl("Volume: 0.45 [MUTED]\n")
	if !ok || v != 0.45 || !muted {
		t.Fatal(v, muted, ok)
	}
	if _, _, ok := parseWpctl("garbage"); ok {
		t.Fatal("parsed garbage")
	}
}
