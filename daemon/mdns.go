package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/godbus/dbus/v5"
)

const serviceType = "_omahass._tcp"

// Advertise publishes the agent through avahi-daemon (already running on
// Omarchy) so Home Assistant's zeroconf discovery offers the PC as a new
// device. It re-publishes when avahi restarts or the device is renamed.
type Advertiser struct {
	cfg   *ConfigStore
	renew chan struct{}
}

func NewAdvertiser(cfg *ConfigStore) *Advertiser {
	return &Advertiser{cfg: cfg, renew: make(chan struct{}, 1)}
}

func (a *Advertiser) Renew() {
	select {
	case a.renew <- struct{}{}:
	default:
	}
}

func (a *Advertiser) Run(ctx context.Context) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Printf("mdns: system bus unavailable, discovery disabled: %v", err)
		return
	}
	defer conn.Close()
	_ = conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, "org.freedesktop.Avahi"))
	sigs := make(chan *dbus.Signal, 8)
	conn.Signal(sigs)

	var group dbus.BusObject
	publish := func() {
		if group != nil {
			group.Call("org.freedesktop.Avahi.EntryGroup.Free", 0)
			group = nil
		}
		g, err := a.publish(conn)
		if err != nil {
			log.Printf("mdns: publish failed (add the PC manually in Home Assistant): %v", err)
			return
		}
		group = g
	}
	publish()
	defer func() {
		if group != nil {
			group.Call("org.freedesktop.Avahi.EntryGroup.Free", 0)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.renew:
			publish()
		case s := <-sigs:
			// Avahi came back: its old entry groups are gone.
			if len(s.Body) == 3 {
				if newOwner, _ := s.Body[2].(string); newOwner != "" {
					time.Sleep(time.Second)
					publish()
				}
			}
		}
	}
}

func (a *Advertiser) publish(conn *dbus.Conn) (dbus.BusObject, error) {
	c := a.cfg.Get()
	server := conn.Object("org.freedesktop.Avahi", "/")
	var path dbus.ObjectPath
	if err := server.Call("org.freedesktop.Avahi.Server.EntryGroupNew", 0).Store(&path); err != nil {
		return nil, err
	}
	group := conn.Object("org.freedesktop.Avahi", path)
	txt := [][]byte{
		[]byte("id=" + c.DeviceID),
		[]byte("name=" + c.DeviceName),
		[]byte(fmt.Sprintf("protocol=%d", agentProtocol)),
		[]byte("version=" + version),
	}
	name := c.DeviceName
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		// interface -1 / protocol -1: all interfaces, IPv4 and IPv6.
		err = group.Call("org.freedesktop.Avahi.EntryGroup.AddService", 0,
			int32(-1), int32(-1), uint32(0), name, serviceType, "", "", uint16(c.AgentPort), txt).Err
		if err == nil {
			break
		}
		name = fmt.Sprintf("%s (%d)", c.DeviceName, attempt+2)
	}
	if err != nil {
		group.Call("org.freedesktop.Avahi.EntryGroup.Free", 0)
		return nil, err
	}
	if err := group.Call("org.freedesktop.Avahi.EntryGroup.Commit", 0).Err; err != nil {
		group.Call("org.freedesktop.Avahi.EntryGroup.Free", 0)
		return nil, err
	}
	log.Printf("mdns: advertising %q as %s on port %d", name, serviceType, c.AgentPort)
	return group, nil
}
