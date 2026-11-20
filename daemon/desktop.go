package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Desktop covers everything that talks to the Omarchy session: Hyprland
// state, audio, brightness, power buttons, custom commands and desktop
// notifications.
type Desktop struct {
	pc        *PC
	cfg       *ConfigStore
	backlight string
	onAction  func(action string, tag string)
}

func NewDesktop(pc *PC, cfg *ConfigStore, onAction func(action, tag string)) *Desktop {
	d := &Desktop{pc: pc, cfg: cfg, onAction: onAction}
	if bl, _ := filepath.Glob("/sys/class/backlight/*"); len(bl) > 0 {
		d.backlight = bl[0]
	}
	d.define()
	d.SyncCustomCommands()
	return d
}

func run(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// detached starts a command that may outlive the request (shutdown,
// launching apps) without waiting for it.
func detached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func btn(fn func() error) Handler { return func(any) error { return fn() } }

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "on" || x == "true" || x == "1"
	case float64:
		return x != 0
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

func (d *Desktop) define() {
	p := d.pc
	p.Define(PCEntity{Key: "active_window", Platform: "sensor", Name: "Active window", Group: "Desktop", Icon: "mdi:application-outline", DefaultOn: true}, nil)
	p.Define(PCEntity{Key: "active_app", Platform: "sensor", Name: "Active app", Group: "Desktop", Icon: "mdi:application", DefaultOn: true}, nil)
	p.Define(PCEntity{Key: "active_workspace", Platform: "sensor", Name: "Workspace", Group: "Desktop", Icon: "mdi:view-grid-outline"}, nil)
	p.Define(PCEntity{Key: "displays_on", Platform: "switch", Name: "Displays", Group: "Desktop", Icon: "mdi:monitor", DefaultOn: true}, func(v any) error {
		state := "off"
		if toBool(v) {
			state = "on"
		}
		if err := run("hyprctl", "dispatch", "dpms", state); err != nil {
			return err
		}
		p.Set("displays_on", state == "on")
		return nil
	})

	p.Define(PCEntity{Key: "volume", Platform: "number", Name: "Volume", Group: "Audio", Icon: "mdi:volume-high", Unit: "%", Min: f64(0), Max: f64(100), Step: f64(1), DefaultOn: true}, func(v any) error {
		f, ok := toFloat(v)
		if !ok {
			return fmt.Errorf("bad volume %v", v)
		}
		f = math.Max(0, math.Min(100, f))
		if err := run("wpctl", "set-volume", "@DEFAULT_AUDIO_SINK@", fmt.Sprintf("%.2f", f/100)); err != nil {
			return err
		}
		p.Set("volume", math.Round(f))
		return nil
	})
	p.Define(PCEntity{Key: "muted", Platform: "switch", Name: "Mute", Group: "Audio", Icon: "mdi:volume-off", DefaultOn: true}, func(v any) error {
		on := "0"
		if toBool(v) {
			on = "1"
		}
		if err := run("wpctl", "set-mute", "@DEFAULT_AUDIO_SINK@", on); err != nil {
			return err
		}
		p.Set("muted", on == "1")
		return nil
	})
	p.Define(PCEntity{Key: "mic_muted", Platform: "switch", Name: "Microphone mute", Group: "Audio", Icon: "mdi:microphone-off"}, func(v any) error {
		on := "0"
		if toBool(v) {
			on = "1"
		}
		if err := run("wpctl", "set-mute", "@DEFAULT_AUDIO_SOURCE@", on); err != nil {
			return err
		}
		p.Set("mic_muted", on == "1")
		return nil
	})

	if d.backlight != "" {
		p.Define(PCEntity{Key: "brightness", Platform: "number", Name: "Screen brightness", Group: "Desktop", Icon: "mdi:brightness-6", Unit: "%", Min: f64(1), Max: f64(100), Step: f64(1), DefaultOn: true}, func(v any) error {
			f, ok := toFloat(v)
			if !ok {
				return fmt.Errorf("bad brightness %v", v)
			}
			f = math.Max(1, math.Min(100, f))
			if err := run("brightnessctl", "-q", "set", fmt.Sprintf("%.0f%%", f)); err != nil {
				return err
			}
			p.Set("brightness", math.Round(f))
			return nil
		})
	}

	p.Define(PCEntity{Key: "lock", Platform: "button", Name: "Lock", Group: "Commands", Icon: "mdi:lock", DefaultOn: true}, btn(func() error { return detached("omarchy-system-lock") }))
	p.Define(PCEntity{Key: "screen_off", Platform: "button", Name: "Screen off", Group: "Commands", Icon: "mdi:monitor-off", DefaultOn: true}, btn(func() error {
		// Give a click-triggered call time to release before blanking.
		time.AfterFunc(500*time.Millisecond, func() {
			if run("hyprctl", "dispatch", "dpms", "off") == nil {
				p.Set("displays_on", false)
			}
		})
		return nil
	}))
	p.Define(PCEntity{Key: "suspend", Platform: "button", Name: "Sleep", Group: "Commands", Icon: "mdi:power-sleep", DefaultOn: true}, btn(func() error { return detached("systemctl", "suspend") }))
	p.Define(PCEntity{Key: "hibernate", Platform: "button", Name: "Hibernate", Group: "Commands", Icon: "mdi:snowflake"}, btn(func() error { return detached("systemctl", "hibernate") }))
	p.Define(PCEntity{Key: "reboot", Platform: "button", Name: "Restart", Group: "Commands", Icon: "mdi:restart", DeviceClass: "restart"}, btn(func() error { return detached("omarchy-system", "reboot") }))
	p.Define(PCEntity{Key: "shutdown", Platform: "button", Name: "Shut down", Group: "Commands", Icon: "mdi:power"}, btn(func() error { return detached("omarchy-system", "shutdown") }))
	p.Define(PCEntity{Key: "logout", Platform: "button", Name: "Log out", Group: "Commands", Icon: "mdi:logout"}, btn(func() error { return detached("omarchy-system", "logout") }))

	p.Define(PCEntity{Key: "notify", Platform: "notify", Name: "Notifications", Group: "Desktop", Icon: "mdi:message-badge", DefaultOn: true}, d.notify)
}

// SyncCustomCommands mirrors config.custom_commands into button entities.
func (d *Desktop) SyncCustomCommands() {
	cfg := d.cfg.Get()
	want := map[string]bool{}
	for _, c := range cfg.CustomCommands {
		if c.ID == "" || strings.TrimSpace(c.Command) == "" {
			continue
		}
		key := "cmd_" + c.ID
		want[key] = true
		icon := c.Icon
		if icon == "" {
			icon = "mdi:console"
		}
		command := c.Command
		d.pc.Define(PCEntity{Key: key, Platform: "button", Name: c.Name, Group: "Custom commands", Icon: icon, DefaultOn: true}, btn(func() error {
			return detached("sh", "-c", command)
		}))
	}
	for _, e := range d.pc.Entities(nil) {
		if strings.HasPrefix(e.Key, "cmd_") && !want[e.Key] {
			d.pc.Remove(e.Key)
		}
	}
}

// notify shows a desktop notification. Actions become buttons; the chosen
// one is reported back to Home Assistant as an event.
func (d *Desktop) notify(v any) error {
	m, _ := v.(map[string]any)
	if m == nil {
		return fmt.Errorf("bad notification payload")
	}
	str := func(k string) string { s, _ := m[k].(string); return s }
	title, message := str("title"), str("message")
	if title == "" {
		title = "Home Assistant"
	}
	args := []string{"-a", "Home Assistant", "-i", "home-assistant"}
	if u := str("urgency"); u == "low" || u == "normal" || u == "critical" {
		args = append(args, "-u", u)
	}
	actions, _ := m["actions"].([]any)
	for _, a := range actions {
		am, _ := a.(map[string]any)
		id, _ := am["action"].(string)
		label, _ := am["title"].(string)
		if id != "" && label != "" {
			args = append(args, "-A", id+"="+label)
		}
	}
	tag := str("tag")
	args = append(args, "--", title, message)
	if len(actions) == 0 {
		return run("notify-send", args...)
	}
	args = append([]string{"--wait"}, args...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		out, err := exec.CommandContext(ctx, "notify-send", args...).Output()
		if err != nil {
			return
		}
		if action := strings.TrimSpace(string(out)); action != "" && d.onAction != nil {
			d.onAction(action, tag)
		}
	}()
	return nil
}

// Poll keeps audio, brightness and display state fresh.
func (d *Desktop) Poll(ctx context.Context, interval func() time.Duration) {
	for {
		d.pollOnce()
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval()):
		}
	}
}

func (d *Desktop) pollOnce() {
	v := map[string]any{}
	if out, err := runOut(2*time.Second, "wpctl", "get-volume", "@DEFAULT_AUDIO_SINK@"); err == nil {
		if vol, muted, ok := parseWpctl(out); ok {
			v["volume"] = math.Round(vol * 100)
			v["muted"] = muted
		}
	}
	if out, err := runOut(2*time.Second, "wpctl", "get-volume", "@DEFAULT_AUDIO_SOURCE@"); err == nil {
		if _, muted, ok := parseWpctl(out); ok {
			v["mic_muted"] = muted
		}
	}
	if d.backlight != "" {
		cur, ok1 := readInt(d.backlight + "/actual_brightness")
		max, ok2 := readInt(d.backlight + "/max_brightness")
		if ok1 && ok2 && max > 0 {
			v["brightness"] = math.Round(100 * float64(cur) / float64(max))
		}
	}
	if out, err := runOut(2*time.Second, "hyprctl", "monitors", "-j"); err == nil {
		var mons []struct {
			DPMS bool `json:"dpmsStatus"`
		}
		if json.Unmarshal([]byte(out), &mons) == nil && len(mons) > 0 {
			on := false
			for _, m := range mons {
				on = on || m.DPMS
			}
			v["displays_on"] = on
		}
	}
	d.pc.SetMany(v)
}

// parseWpctl reads "Volume: 0.45" or "Volume: 0.45 [MUTED]".
func parseWpctl(out string) (float64, bool, bool) {
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != "Volume:" {
		return 0, false, false
	}
	vol, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, false, false
	}
	return vol, strings.Contains(out, "[MUTED]"), true
}

// WatchHyprland follows Hyprland's event socket for the focused window and
// workspace, so those sensors update instantly without polling.
func (d *Desktop) WatchHyprland(ctx context.Context) {
	sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if sig == "" || runtime == "" {
		log.Printf("hyprland: not running under Hyprland, desktop sensors disabled")
		return
	}
	sock := filepath.Join(runtime, "hypr", sig, ".socket2.sock")
	d.refreshActive()
	for ctx.Err() == nil {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}
		go func() { <-ctx.Done(); conn.Close() }()
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			ev, data, _ := strings.Cut(sc.Text(), ">>")
			switch ev {
			case "activewindow":
				class, title, _ := strings.Cut(data, ",")
				d.pc.SetMany(map[string]any{"active_app": emptyNil(class), "active_window": emptyNil(truncate(title, 250))})
			case "workspacev2":
				_, name, _ := strings.Cut(data, ",")
				d.pc.Set("active_workspace", name)
			case "focusedmonv2":
				parts := strings.Split(data, ",")
				if len(parts) >= 3 {
					d.pc.Set("active_workspace", parts[2])
				}
			}
		}
		conn.Close()
	}
}

func (d *Desktop) refreshActive() {
	if out, err := runOut(2*time.Second, "hyprctl", "activewindow", "-j"); err == nil {
		var w struct {
			Class string `json:"class"`
			Title string `json:"title"`
		}
		if json.Unmarshal([]byte(out), &w) == nil {
			d.pc.SetMany(map[string]any{"active_app": emptyNil(w.Class), "active_window": emptyNil(truncate(w.Title, 250))})
		}
	}
	if out, err := runOut(2*time.Second, "hyprctl", "activeworkspace", "-j"); err == nil {
		var ws struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(out), &ws) == nil {
			d.pc.Set("active_workspace", ws.Name)
		}
	}
}

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// truncate keeps Home Assistant's 255-character state limit happy.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
