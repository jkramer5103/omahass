package main

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync"
)

// PCEntity describes one thing this PC offers to Home Assistant: a sensor
// reading, a command button, a switch, a slider, the media player or the
// notification target.
type PCEntity struct {
	Key         string   `json:"key"`
	Platform    string   `json:"platform"` // sensor | binary_sensor | button | switch | number | media_player | notify
	Name        string   `json:"name"`
	Group       string   `json:"group"` // UI grouping in the bar panel
	Icon        string   `json:"icon,omitempty"`
	DeviceClass string   `json:"device_class,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	StateClass  string   `json:"state_class,omitempty"`
	Category    string   `json:"entity_category,omitempty"` // diagnostic | config
	Precision   *int     `json:"precision,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Step        *float64 `json:"step,omitempty"`
	DefaultOn   bool     `json:"default_on"`
	// Dangerous commands ask for nothing on the PC side; they're just off by
	// default so a fresh pairing can't reboot the machine by accident.
	Enabled bool `json:"enabled"`
}

func prec(n int) *int        { return &n }
func f64(v float64) *float64 { return &v }

// Handler runs a Home Assistant command against an entity. value is nil for
// buttons, bool for switches, float64 for numbers and a map for media/notify.
type Handler func(value any) error

type PC struct {
	mu       sync.RWMutex
	order    []string
	entities map[string]PCEntity
	states   map[string]any
	handlers map[string]Handler
	onChange func(changes map[string]any)
	onSchema func()
}

func NewPC() *PC {
	return &PC{
		entities: map[string]PCEntity{},
		states:   map[string]any{},
		handlers: map[string]Handler{},
	}
}

// Define registers (or replaces) an entity. Order of first definition is
// the display order.
func (p *PC) Define(e PCEntity, h Handler) {
	p.mu.Lock()
	if _, ok := p.entities[e.Key]; !ok {
		p.order = append(p.order, e.Key)
	}
	p.entities[e.Key] = e
	if h != nil {
		p.handlers[e.Key] = h
	}
	cb := p.onSchema
	p.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// Remove drops an entity, e.g. a deleted custom command.
func (p *PC) Remove(key string) {
	p.mu.Lock()
	if _, ok := p.entities[key]; !ok {
		p.mu.Unlock()
		return
	}
	delete(p.entities, key)
	delete(p.states, key)
	delete(p.handlers, key)
	for i, k := range p.order {
		if k == key {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	cb := p.onSchema
	p.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func normalize(v any) any {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return math.Round(x*100) / 100
	case float32:
		return normalize(float64(x))
	}
	return v
}

// Set updates a state value and reports it to subscribers only if it
// actually changed.
func (p *PC) Set(key string, v any) {
	p.SetMany(map[string]any{key: v})
}

func (p *PC) SetMany(values map[string]any) {
	changes := map[string]any{}
	p.mu.Lock()
	for k, v := range values {
		v = normalize(v)
		if old, ok := p.states[k]; ok && reflect.DeepEqual(old, v) {
			continue
		}
		p.states[k] = v
		changes[k] = v
	}
	cb := p.onChange
	p.mu.Unlock()
	if len(changes) > 0 && cb != nil {
		cb(changes)
	}
}

func (p *PC) State(key string) any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.states[key]
}

// Entities returns all entities in display order, with Enabled resolved
// against the expose map.
func (p *PC) Entities(expose map[string]bool) []PCEntity {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]PCEntity, 0, len(p.order))
	for _, k := range p.order {
		e := p.entities[k]
		e.Enabled = e.DefaultOn
		if v, ok := expose[k]; ok {
			e.Enabled = v
		}
		out = append(out, e)
	}
	return out
}

func (p *PC) Exposed(expose map[string]bool) []PCEntity {
	all := p.Entities(expose)
	out := all[:0]
	for _, e := range all {
		if e.Enabled {
			out = append(out, e)
		}
	}
	return out
}

func (p *PC) States(keys []string) map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := p.states[k]; ok {
			out[k] = v
		} else {
			out[k] = nil
		}
	}
	return out
}

func (p *PC) AllStates() map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]any, len(p.states))
	for k, v := range p.states {
		out[k] = v
	}
	return out
}

func (p *PC) Run(key string, value any) error {
	p.mu.RLock()
	h, ok := p.handlers[key]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no handler for %q", key)
	}
	return h(value)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
