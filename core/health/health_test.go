package health

import (
	"testing"
	"time"
)

func TestUnknownAndProbeFailureAreNotHealthy(t *testing.T) {
	store := NewStore()
	if state := store.Get("missing"); state.Status != "unknown" || state.Score != 0 {
		t.Fatalf("unexpected missing state: %#v", state)
	}
	state := store.RecordProbe("realm", "healthy", 12.5, "probe failed")
	if state.Status != "failed" || state.Score != 0 || state.Error == "" {
		t.Fatalf("probe error was not retained: %#v", state)
	}
}

func TestHealthStateBecomesStaleAndRecoversOnProbe(t *testing.T) {
	store := NewStore()
	store.SetStaleAfter(time.Millisecond)
	store.RecordProbe("realm", "healthy", 2, "")
	time.Sleep(3 * time.Millisecond)
	if state := store.Get("realm"); state.Status != "stale" || state.Score != 0 || !state.Stale {
		t.Fatalf("expected stale state, got %#v", state)
	}
	state := store.RecordProbe("realm", "healthy", 1, "")
	if state.Status != "healthy" || state.Stale || state.Score != 100 {
		t.Fatalf("probe should recover stale state, got %#v", state)
	}
}
