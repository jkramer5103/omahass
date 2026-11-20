package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// CustomCommand is a user-defined shell command that shows up in Home
// Assistant as a button on the PC device.
type CustomCommand struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
	Icon    string `json:"icon,omitempty"`
}

// Config is everything omahassd persists. It lives in
// ~/.config/omahass/config.json and is edited from the bar panel.
type Config struct {
	// Outbound connection to Home Assistant, used by the bar panel.
	HAURL   string `json:"ha_url"`
	HAToken string `json:"ha_token"`

	// Favorites shown in the bar panel, in display order.
	Favorites []string `json:"favorites"`

	// Inbound agent server that the Home Assistant integration connects to.
	DeviceName string `json:"device_name"`
	DeviceID   string `json:"device_id"`
	AgentPort  int    `json:"agent_port"`
	AgentKey   string `json:"agent_key"`

	// Which PC entities are exposed to Home Assistant. Missing keys fall
	// back to the entity's default.
	Expose map[string]bool `json:"expose"`

	CustomCommands []CustomCommand `json:"custom_commands"`

	// Seconds between fast sensor samples (CPU, memory, network...).
	PollSeconds int `json:"poll_seconds"`
}

type ConfigStore struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

func defaultConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "omahass", "config.json")
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func LoadConfig(path string) (*ConfigStore, error) {
	s := &ConfigStore{path: path}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &s.cfg); err != nil {
			return nil, err
		}
	}
	changed := s.applyDefaults()
	if changed || len(data) == 0 {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *ConfigStore) applyDefaults() bool {
	c := &s.cfg
	changed := false
	if c.DeviceName == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "omarchy"
		}
		c.DeviceName = host
		changed = true
	}
	if c.DeviceID == "" {
		c.DeviceID = randomHex(6)
		changed = true
	}
	if c.AgentPort == 0 {
		c.AgentPort = 47811
		changed = true
	}
	if c.AgentKey == "" {
		// Short enough to type into Home Assistant's config flow.
		c.AgentKey = randomHex(4)
		changed = true
	}
	if c.Expose == nil {
		c.Expose = map[string]bool{}
		changed = true
	}
	if c.Favorites == nil {
		c.Favorites = []string{}
		changed = true
	}
	if c.CustomCommands == nil {
		c.CustomCommands = []CustomCommand{}
		changed = true
	}
	if c.PollSeconds <= 0 {
		c.PollSeconds = 5
		changed = true
	}
	return changed
}

func (s *ConfigStore) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Favorites = append([]string(nil), s.cfg.Favorites...)
	c.CustomCommands = append([]CustomCommand(nil), s.cfg.CustomCommands...)
	c.Expose = make(map[string]bool, len(s.cfg.Expose))
	for k, v := range s.cfg.Expose {
		c.Expose[k] = v
	}
	return c
}

// Update applies fn to the config under the lock and persists the result.
func (s *ConfigStore) Update(fn func(*Config)) (Config, error) {
	s.mu.Lock()
	fn(&s.cfg)
	s.cfg.HAURL = strings.TrimRight(strings.TrimSpace(s.cfg.HAURL), "/")
	s.applyDefaults()
	err := s.saveLocked()
	s.mu.Unlock()
	return s.Get(), err
}

func (s *ConfigStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
