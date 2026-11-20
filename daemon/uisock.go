package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UIServer speaks NDJSON with the bar panel over a unix socket. The panel
// reaches it through `omahassd attach`, a tiny stdin/stdout relay, because
// Quickshell's Process is the most robust transport QML has.
type UIServer struct {
	d       *Daemon
	mu      sync.Mutex
	clients map[*uiClient]struct{}
}

type uiClient struct {
	send chan []byte
}

func socketPath() string {
	if p := os.Getenv("OMAHASS_SOCKET"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "omahass.sock")
}

func NewUIServer(d *Daemon) *UIServer {
	return &UIServer{d: d, clients: map[*uiClient]struct{}{}}
}

func (u *UIServer) Serve(ctx context.Context) error {
	path := socketPath()
	// A stale socket from a crashed run would block Listen.
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		log.Fatalf("ui: another omahassd is already running (%s)", path)
	}
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	os.Chmod(path, 0o600)
	go func() { <-ctx.Done(); ln.Close(); os.Remove(path) }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go u.serve(ctx, conn)
	}
}

func (u *UIServer) serve(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	c := &uiClient{send: make(chan []byte, 512)}
	u.mu.Lock()
	u.clients[c] = struct{}{}
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		delete(u.clients, c)
		u.mu.Unlock()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		w := bufio.NewWriter(conn)
		for {
			select {
			case <-ctx.Done():
				return
			case b, ok := <-c.send:
				if !ok {
					return
				}
				w.Write(b)
				w.WriteByte('\n')
				// Drain whatever else is queued before flushing.
				for n := len(c.send); n > 0; n-- {
					w.Write(<-c.send)
					w.WriteByte('\n')
				}
				if w.Flush() != nil {
					return
				}
			}
		}
	}()

	u.sendAll(c)
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		var op map[string]any
		if json.Unmarshal(sc.Bytes(), &op) != nil {
			continue
		}
		u.d.handleOp(c, op)
	}
	conn.Close()
	<-done
}

func (u *UIServer) to(c *uiClient, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- b:
	default:
		// Panel is wedged; drop rather than block the daemon.
	}
}

func (u *UIServer) broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for c := range u.clients {
		select {
		case c.send <- b:
		default:
		}
	}
}

func (u *UIServer) hasClients() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.clients) > 0
}

// sendAll brings a freshly attached panel fully up to date.
func (u *UIServer) sendAll(c *uiClient) {
	u.to(c, map[string]any{"ev": "hello", "version": version})
	u.to(c, u.d.statusEvent())
	u.to(c, u.d.favoritesEvent())
	u.to(c, u.d.pcEvent())
	u.to(c, u.d.configEvent())
}

// Attach relays stdin/stdout to the daemon socket, reconnecting forever.
// While the daemon is down it tells the panel so, once per outage.
func Attach() {
	stdin := make(chan []byte, 64)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			stdin <- b
		}
		os.Exit(0) // the shell went away
	}()
	out := bufio.NewWriter(os.Stdout)
	announced := false
	for {
		conn, err := net.Dial("unix", socketPath())
		if err != nil {
			if !announced {
				out.WriteString(`{"ev":"daemon","running":false}` + "\n")
				out.Flush()
				announced = true
			}
			// Drop panel commands while there's no daemon to take them.
			deadline := time.After(2 * time.Second)
		drain:
			for {
				select {
				case <-stdin:
				case <-deadline:
					break drain
				}
			}
			continue
		}
		announced = false
		out.WriteString(`{"ev":"daemon","running":true}` + "\n")
		out.Flush()
		closed := make(chan struct{})
		go func() {
			io.Copy(os.Stdout, conn)
			close(closed)
		}()
	pump:
		for {
			select {
			case b := <-stdin:
				if _, err := conn.Write(append(b, '\n')); err != nil {
					break pump
				}
			case <-closed:
				break pump
			}
		}
		conn.Close()
		<-closed
	}
}

func strOf(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}
