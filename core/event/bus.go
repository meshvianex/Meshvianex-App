package event

import (
	"sync"
	"time"
)

type Event struct {
	Type    string         `json:"type"`
	Version int            `json:"version"`
	Time    time.Time      `json:"time"`
	Data    map[string]any `json:"data,omitempty"`
}

type Bus struct {
	mu   sync.RWMutex
	next int
	subs map[int]subscription
}

type subscription struct {
	ch      chan Event
	allowed map[string]bool
}

func NewBus() *Bus { return &Bus{subs: map[int]subscription{}} }

func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if e.Version == 0 {
		e.Version = 1
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs {
		if len(sub.allowed) > 0 && !sub.allowed[e.Type] {
			continue
		}
		select {
		case sub.ch <- e:
		default:
		}
	}
}

func (b *Bus) SubscribeTypes(buffer int, types ...string) (<-chan Event, func()) {
	if len(types) == 0 {
		return b.Subscribe(buffer)
	}
	out := make(chan Event, buffer)
	allowed := map[string]bool{}
	for _, typ := range types {
		allowed[typ] = true
	}
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = subscription{ch: out, allowed: allowed}
	b.mu.Unlock()
	return out, b.cancel(id)
}

func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan Event, buffer)
	b.subs[id] = subscription{ch: ch}
	return ch, b.cancel(id)
}

func (b *Bus) cancel(id int) func() {
	return func() {
		b.mu.Lock()
		if sub, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(sub.ch)
		}
		b.mu.Unlock()
	}
}
