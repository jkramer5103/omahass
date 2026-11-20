package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	mprisPrefix = "org.mpris.MediaPlayer2."
	mprisPath   = "/org/mpris/MediaPlayer2"
	mprisPlayer = "org.mpris.MediaPlayer2.Player"
)

// Media follows MPRIS players over D-Bus and publishes the most relevant
// one (playing beats paused beats stopped) as a Home Assistant media player.
type Media struct {
	pc      *PC
	conn    *dbus.Conn
	mu      sync.Mutex
	current string // bus name of the player we're showing
	lastArt string
	trackID dbus.ObjectPath
	kick    chan struct{}
}

func NewMedia(pc *PC) *Media {
	m := &Media{pc: pc, kick: make(chan struct{}, 1)}
	pc.Define(PCEntity{Key: "media", Platform: "media_player", Name: "Media", Group: "Audio", Icon: "mdi:music", DefaultOn: true}, m.command)
	return m
}

func (m *Media) Run(ctx context.Context) {
	for ctx.Err() == nil {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			log.Printf("media: session bus: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
				continue
			}
		}
		m.mu.Lock()
		m.conn = conn
		m.mu.Unlock()
		m.loop(ctx, conn)
		conn.Close()
	}
}

func (m *Media) loop(ctx context.Context, conn *dbus.Conn) {
	_ = conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus.Properties"), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchObjectPath(mprisPath))
	_ = conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg0Namespace("org.mpris.MediaPlayer2"))
	_ = conn.AddMatchSignal(dbus.WithMatchInterface(mprisPlayer), dbus.WithMatchMember("Seeked"))
	sigs := make(chan *dbus.Signal, 32)
	conn.Signal(sigs)
	defer conn.RemoveSignal(sigs)

	m.refresh(conn)
	poll := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-sigs:
			if !ok {
				return
			}
			_ = sig
			if debounce == nil {
				debounce = time.After(150 * time.Millisecond)
			}
		case <-m.kick:
			if debounce == nil {
				debounce = time.After(150 * time.Millisecond)
			}
		case <-debounce:
			debounce = nil
			m.refresh(conn)
		case <-poll.C:
			if !conn.Connected() {
				return
			}
			m.refresh(conn)
		}
	}
}

type playerInfo struct {
	name   string
	status string
	props  map[string]dbus.Variant
}

func (m *Media) players(conn *dbus.Conn) []playerInfo {
	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return nil
	}
	var out []playerInfo
	for _, n := range names {
		if !strings.HasPrefix(n, mprisPrefix) || n == mprisPrefix+"playerctld" {
			continue
		}
		var props map[string]dbus.Variant
		obj := conn.Object(n, mprisPath)
		if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, mprisPlayer).Store(&props); err != nil {
			continue
		}
		status, _ := props["PlaybackStatus"].Value().(string)
		out = append(out, playerInfo{name: n, status: status, props: props})
	}
	return out
}

func (m *Media) refresh(conn *dbus.Conn) {
	players := m.players(conn)
	m.mu.Lock()
	prev := m.current
	m.mu.Unlock()

	rank := func(p playerInfo) int {
		r := 0
		switch p.status {
		case "Playing":
			r = 30
		case "Paused":
			r = 20
		default:
			r = 10
		}
		if p.name == prev {
			r += 5 // stickiness so two paused players don't flip-flop
		}
		return r
	}
	var best *playerInfo
	for i := range players {
		if best == nil || rank(players[i]) > rank(*best) {
			best = &players[i]
		}
	}
	if best == nil {
		m.mu.Lock()
		m.current = ""
		m.mu.Unlock()
		m.pc.Set("media", map[string]any{"state": "off"})
		return
	}

	meta, _ := best.props["Metadata"].Value().(map[string]dbus.Variant)
	str := func(k string) string { s, _ := meta[k].Value().(string); return s }
	artists := ""
	if a, ok := meta["xesam:artist"].Value().([]string); ok {
		artists = strings.Join(a, ", ")
	}
	length := int64Of(meta["mpris:length"])
	position := int64Of(best.props["Position"])
	state := map[string]any{
		"state":    strings.ToLower(best.status),
		"player":   playerName(best.name),
		"title":    str("xesam:title"),
		"artist":   artists,
		"album":    str("xesam:album"),
		"art_url":  str("mpris:artUrl"),
		"duration": float64(length) / 1e6,
		"position": float64(position) / 1e6,
		// Rounded so a steadily advancing position doesn't count as a
		// change on every poll; HA extrapolates from position_updated_at.
		"position_updated_at": time.Now().UTC().Truncate(5 * time.Second).Format(time.RFC3339),
	}
	if state["state"] == "stopped" {
		state["state"] = "idle"
	}
	if vol, ok := best.props["Volume"].Value().(float64); ok {
		state["volume"] = vol
	}
	if s, ok := best.props["Shuffle"].Value().(bool); ok {
		state["shuffle"] = s
	}
	if tid, ok := meta["mpris:trackid"].Value().(dbus.ObjectPath); ok {
		m.mu.Lock()
		m.trackID = tid
		m.mu.Unlock()
	}
	// Positions only matter while playing; freeze them otherwise so a
	// paused player stays quiet.
	if state["state"] != "playing" {
		delete(state, "position_updated_at")
	}
	m.mu.Lock()
	m.current = best.name
	m.lastArt = str("mpris:artUrl")
	m.mu.Unlock()
	m.pc.Set("media", state)
}

func int64Of(v dbus.Variant) int64 {
	switch x := v.Value().(type) {
	case int64:
		return x
	case uint64:
		return int64(x)
	case int32:
		return int64(x)
	case uint32:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func playerName(bus string) string {
	n := strings.TrimPrefix(bus, mprisPrefix)
	if i := strings.Index(n, ".instance"); i > 0 {
		n = n[:i]
	}
	if n == "" {
		return n
	}
	return strings.ToUpper(n[:1]) + n[1:]
}

// ArtURL is the current track's artwork URL (file:// or http(s)://).
func (m *Media) ArtURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastArt
}

func (m *Media) command(v any) error {
	cmd, _ := v.(map[string]any)
	action, _ := cmd["action"].(string)
	m.mu.Lock()
	conn, name, track := m.conn, m.current, m.trackID
	m.mu.Unlock()
	if conn == nil || name == "" {
		return fmt.Errorf("no media player")
	}
	obj := conn.Object(name, mprisPath)
	var call *dbus.Call
	switch action {
	case "play_pause":
		call = obj.Call(mprisPlayer+".PlayPause", 0)
	case "play":
		call = obj.Call(mprisPlayer+".Play", 0)
	case "pause":
		call = obj.Call(mprisPlayer+".Pause", 0)
	case "stop":
		call = obj.Call(mprisPlayer+".Stop", 0)
	case "next":
		call = obj.Call(mprisPlayer+".Next", 0)
	case "previous":
		call = obj.Call(mprisPlayer+".Previous", 0)
	case "volume":
		f, ok := toFloat(cmd["value"])
		if !ok {
			return fmt.Errorf("bad volume")
		}
		call = obj.Call("org.freedesktop.DBus.Properties.Set", 0, mprisPlayer, "Volume", dbus.MakeVariant(f))
	case "shuffle":
		call = obj.Call("org.freedesktop.DBus.Properties.Set", 0, mprisPlayer, "Shuffle", dbus.MakeVariant(toBool(cmd["value"])))
	case "seek":
		f, ok := toFloat(cmd["value"])
		if !ok || track == "" {
			return fmt.Errorf("cannot seek")
		}
		call = obj.Call(mprisPlayer+".SetPosition", 0, track, int64(f*1e6))
	default:
		return fmt.Errorf("unknown media action %q", action)
	}
	select {
	case m.kick <- struct{}{}:
	default:
	}
	return call.Err
}
