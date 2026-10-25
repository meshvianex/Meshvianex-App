package event

import (
	"testing"
	"time"
)

func TestSubscribeTypesFiltersBeforeBuffering(t *testing.T) {
	bus := NewBus()
	ch, cancel := bus.SubscribeTypes(1, "ui.state.changed")
	defer cancel()

	for n := 0; n < 100; n++ {
		bus.Publish(Event{Type: "flow.route"})
	}
	bus.Publish(Event{Type: "ui.state.changed"})

	select {
	case got := <-ch:
		if got.Type != "ui.state.changed" {
			t.Fatalf("received %q, want UI state event", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("filtered event was not delivered")
	}
}

func TestSubscribeCancelClosesChannel(t *testing.T) {
	bus := NewBus()
	ch, cancel := bus.Subscribe(1)
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("cancelled subscription channel is still open")
	}
}
