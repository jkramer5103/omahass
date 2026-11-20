// omahassd connects an Omarchy PC to Home Assistant, both ways:
//
//   - as a client of Home Assistant's WebSocket API, feeding the bar panel
//     (favorites, live states, service calls);
//   - as a server the omahass Home Assistant integration connects to, which
//     shows the PC as a device with sensors, buttons, switches, a media
//     player and a notify target.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

var version = "0.1.0"

type Daemon struct {
	cfg     *ConfigStore
	pc      *PC
	ha      *HAClient
	agent   *AgentServer
	ui      *UIServer
	desktop *Desktop
	adv     *Advertiser
	pcMedia *Media
	sens    *Sensors
	noMDNS  bool

	schemaMu    sync.Mutex
	schemaTimer *time.Timer
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `omahassd %s

Usage:
  omahassd [run]        run the daemon (default)
  omahassd attach       relay the bar panel's stdin/stdout to the daemon
  omahassd key          print the pairing key for Home Assistant
  omahassd version

Flags:
`, version)
		flag.PrintDefaults()
	}
	configPath := flag.String("config", defaultConfigPath(), "config file")
	noMDNS := flag.Bool("no-mdns", false, "don't advertise the agent over mDNS")
	flag.Parse()

	cmd := flag.Arg(0)
	switch cmd {
	case "attach":
		Attach()
		return
	case "version":
		fmt.Println(version)
		return
	case "key":
		cfg, err := LoadConfig(*configPath)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(cfg.Get().AgentKey)
		return
	case "", "run":
	default:
		flag.Usage()
		os.Exit(2)
	}

	// A soft ceiling for the Go heap; systemd's MemoryMax is the hard one.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(48 << 20)
	}
	log.SetFlags(0)

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	d := newDaemon(cfg)
	d.noMDNS = *noMDNS
	d.start(ctx)
	<-ctx.Done()
	log.Printf("shutting down")
	time.Sleep(200 * time.Millisecond) // let mDNS withdraw
}

func newDaemon(cfg *ConfigStore) *Daemon {
	d := &Daemon{cfg: cfg, pc: NewPC()}
	d.ui = NewUIServer(d)
	d.ha = NewHAClient(cfg)
	d.adv = NewAdvertiser(cfg)

	d.sens = NewSensors(d.pc)
	media := NewMedia(d.pc)
	d.desktop = NewDesktop(d.pc, cfg, func(action, tag string) {
		d.agent.Event("notification_action", map[string]any{"action": action, "tag": tag})
	})
	d.agent = NewAgentServer(cfg, d.pc, media)
	d.agent.onCount = func(int) { d.ui.broadcast(d.statusEvent()) }

	// Wire callbacks after construction so the initial Define calls don't
	// fan out to nobody.
	d.pc.mu.Lock()
	d.pc.onChange = func(changes map[string]any) {
		d.agent.StatesChanged(changes)
		d.ui.broadcast(map[string]any{"ev": "pc_states", "states": changes})
	}
	d.pc.onSchema = d.schemaChanged
	d.pc.mu.Unlock()

	d.ha.OnPhase = func() { d.ui.broadcast(d.statusEvent()) }
	d.ha.OnSnapshot = func() { d.ui.broadcast(d.favoritesEvent()) }
	d.ha.OnState = func(e map[string]any) {
		id, _ := e["entity_id"].(string)
		if slices.Contains(d.cfg.Get().Favorites, id) {
			d.ui.broadcast(map[string]any{"ev": "state", "entity": e})
		}
	}
	d.ha.OnRemoved = func(id string) {
		if slices.Contains(d.cfg.Get().Favorites, id) {
			d.ui.broadcast(d.favoritesEvent())
		}
	}
	d.pcMedia = media
	return d
}

func (d *Daemon) start(ctx context.Context) {
	interval := func() time.Duration { return time.Duration(d.cfg.Get().PollSeconds) * time.Second }
	go func() {
		if err := d.ui.Serve(ctx); err != nil {
			log.Fatalf("ui: %v", err)
		}
	}()
	go func() {
		if err := d.agent.Serve(ctx); err != nil {
			log.Fatalf("agent: %v", err)
		}
	}()
	go d.ha.Run(ctx)
	if !d.noMDNS {
		go d.adv.Run(ctx)
	}
	go d.pcMedia.Run(ctx)
	go d.desktop.WatchHyprland(ctx)
	go d.desktop.Poll(ctx, interval)
	go d.sens.Run(ctx, interval)
}

// schemaChanged coalesces bursts of Define/Remove/expose changes into one
// entity-list push.
func (d *Daemon) schemaChanged() {
	d.schemaMu.Lock()
	defer d.schemaMu.Unlock()
	if d.schemaTimer != nil {
		d.schemaTimer.Stop()
	}
	d.schemaTimer = time.AfterFunc(300*time.Millisecond, func() {
		d.agent.SchemaChanged()
		d.ui.broadcast(d.pcEvent())
	})
}

func (d *Daemon) statusEvent() map[string]any {
	c := d.cfg.Get()
	phase, lastErr, name := d.ha.Status()
	return map[string]any{
		"ev": "status",
		"ha": map[string]any{
			"phase": phase, "error": lastErr, "name": name,
			"url": c.HAURL, "has_token": c.HAToken != "",
		},
		"agent": map[string]any{
			"port": c.AgentPort, "key": c.AgentKey,
			"device_name": c.DeviceName, "device_id": c.DeviceID,
			"clients": d.agent.Clients(), "ip": displayIP(),
		},
	}
}

func (d *Daemon) favoritesEvent() map[string]any {
	favs := d.cfg.Get().Favorites
	out := make([]map[string]any, 0, len(favs))
	for _, id := range favs {
		if e := d.ha.Entity(id); e != nil {
			out = append(out, e)
		} else {
			domain, _, _ := strings.Cut(id, ".")
			out = append(out, map[string]any{"entity_id": id, "domain": domain, "name": id, "state": "unavailable", "attributes": map[string]any{}})
		}
	}
	return map[string]any{"ev": "favorites", "entities": out}
}

func (d *Daemon) pcEvent() map[string]any {
	return map[string]any{"ev": "pc", "entities": d.pc.Entities(d.cfg.Get().Expose), "states": d.pc.AllStates()}
}

func (d *Daemon) configEvent() map[string]any {
	c := d.cfg.Get()
	return map[string]any{"ev": "config", "custom_commands": c.CustomCommands, "poll_seconds": c.PollSeconds, "favorites": c.Favorites}
}

func (d *Daemon) result(c *uiClient, tag string, err error) {
	if tag == "" && err == nil {
		return
	}
	res := map[string]any{"ev": "result", "tag": tag, "ok": err == nil}
	if err != nil {
		res["error"] = err.Error()
	}
	d.ui.to(c, res)
}

func (d *Daemon) handleOp(c *uiClient, op map[string]any) {
	tag := strOf(op, "tag")
	switch strOf(op, "op") {
	case "refresh":
		d.ui.sendAll(c)

	case "call":
		data, _ := op["data"].(map[string]any)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			err := d.ha.CallService(ctx, strOf(op, "domain"), strOf(op, "service"), strOf(op, "entity_id"), data)
			d.result(c, tag, err)
		}()

	case "list":
		query := strings.ToLower(strOf(op, "query"))
		domain := strOf(op, "domain")
		favs := d.cfg.Get().Favorites
		out := []map[string]any{}
		domains := map[string]int{}
		for _, e := range d.ha.List() {
			dom, _ := e["domain"].(string)
			domains[dom]++
			if domain != "" && domain != "all" && dom != domain {
				continue
			}
			if query != "" {
				name, _ := e["name"].(string)
				id, _ := e["entity_id"].(string)
				if !strings.Contains(strings.ToLower(name), query) && !strings.Contains(id, query) {
					continue
				}
			}
			if len(out) < 300 {
				out = append(out, e)
			}
		}
		d.ui.to(c, map[string]any{"ev": "list", "tag": tag, "entities": out, "domains": domains, "favorites": favs})

	case "favorite":
		id := strOf(op, "entity_id")
		on, _ := op["on"].(bool)
		_, err := d.cfg.Update(func(cfg *Config) {
			i := slices.Index(cfg.Favorites, id)
			if on && i < 0 && id != "" {
				cfg.Favorites = append(cfg.Favorites, id)
			} else if !on && i >= 0 {
				cfg.Favorites = slices.Delete(cfg.Favorites, i, i+1)
			}
		})
		d.result(c, tag, err)
		d.ui.broadcast(d.favoritesEvent())
		d.ui.broadcast(d.configEvent())

	case "move_favorite":
		id := strOf(op, "entity_id")
		delta, _ := op["delta"].(float64)
		_, err := d.cfg.Update(func(cfg *Config) {
			i := slices.Index(cfg.Favorites, id)
			j := i + int(delta)
			if i >= 0 && j >= 0 && j < len(cfg.Favorites) {
				cfg.Favorites[i], cfg.Favorites[j] = cfg.Favorites[j], cfg.Favorites[i]
			}
		})
		d.result(c, tag, err)
		d.ui.broadcast(d.favoritesEvent())

	case "set_ha":
		url, token := strOf(op, "url"), strOf(op, "token")
		_, err := d.cfg.Update(func(cfg *Config) {
			cfg.HAURL = url
			if token != "" {
				cfg.HAToken = token
			}
			if url == "" {
				cfg.HAToken = ""
			}
		})
		d.result(c, tag, err)
		d.ha.Reconfigure()
		d.ui.broadcast(d.statusEvent())

	case "set_device":
		name := strOf(op, "name")
		if name == "" {
			d.result(c, tag, fmt.Errorf("name can't be empty"))
			return
		}
		_, err := d.cfg.Update(func(cfg *Config) { cfg.DeviceName = name })
		d.result(c, tag, err)
		d.adv.Renew()
		d.agent.SchemaChanged()
		d.ui.broadcast(d.statusEvent())

	case "regen_key":
		_, err := d.cfg.Update(func(cfg *Config) { cfg.AgentKey = randomHex(4) })
		d.result(c, tag, err)
		d.ui.broadcast(d.statusEvent())

	case "expose":
		key := strOf(op, "key")
		on, _ := op["on"].(bool)
		_, err := d.cfg.Update(func(cfg *Config) { cfg.Expose[key] = on })
		d.result(c, tag, err)
		d.schemaChanged()

	case "run_pc":
		key := strOf(op, "key")
		value := op["value"]
		go func() { d.result(c, tag, d.pc.Run(key, value)) }()

	case "add_command":
		name, command := strOf(op, "name"), strOf(op, "command")
		if name == "" || command == "" {
			d.result(c, tag, fmt.Errorf("name and command are required"))
			return
		}
		_, err := d.cfg.Update(func(cfg *Config) {
			cfg.CustomCommands = append(cfg.CustomCommands, CustomCommand{ID: randomHex(3), Name: name, Command: command, Icon: strOf(op, "icon")})
		})
		d.result(c, tag, err)
		d.desktop.SyncCustomCommands()
		d.ui.broadcast(d.configEvent())

	case "remove_command":
		id := strOf(op, "id")
		_, err := d.cfg.Update(func(cfg *Config) {
			cfg.CustomCommands = slices.DeleteFunc(cfg.CustomCommands, func(cc CustomCommand) bool { return cc.ID == id })
			delete(cfg.Expose, "cmd_"+id)
		})
		d.result(c, tag, err)
		d.desktop.SyncCustomCommands()
		d.ui.broadcast(d.configEvent())

	case "set_poll":
		secs, _ := op["seconds"].(float64)
		if secs < 1 || secs > 300 {
			d.result(c, tag, fmt.Errorf("interval must be 1–300 seconds"))
			return
		}
		_, err := d.cfg.Update(func(cfg *Config) { cfg.PollSeconds = int(secs) })
		d.result(c, tag, err)
		d.ui.broadcast(d.configEvent())
	}
}


// displayIP is the address shown in the panel. OMAHASS_DISPLAY_IP overrides
// it, for screenshots.
func displayIP() any {
	if ip := os.Getenv("OMAHASS_DISPLAY_IP"); ip != "" {
		return ip
	}
	return localIP()
}
