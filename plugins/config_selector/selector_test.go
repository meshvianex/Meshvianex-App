package config_selector

import (
	"context"
	"sync"
	"testing"
	"time"

	"meshvianex/core/netx"
	"meshvianex/core/plugin"
)

func TestRoundRobinIsDeterministicAndConcurrentSafe(t *testing.T) {
	selector := &roundRobinSelector{}
	flow := netx.Flow{Host: "example.com", Port: 443}
	ordered := []string{"cfg-b", "cfg-a"}
	for _, want := range []string{"cfg-a", "cfg-b", "cfg-a", "cfg-b"} {
		got, err := selector.Select(context.Background(), flow, ordered)
		if err != nil || got != want {
			t.Fatalf("got %q, want %q, err=%v", got, want, err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := selector.Select(context.Background(), flow, ordered); err != nil {
				t.Errorf("concurrent select: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestRoundRobinCounterWrapsWithoutChangingCandidateSafety(t *testing.T) {
	selector := &roundRobinSelector{next: ^uint64(0)}
	first, err := selector.Select(context.Background(), netx.Flow{}, []string{"a", "b"})
	if err != nil || first != "b" {
		t.Fatalf("wrapped round robin first=%q err=%v", first, err)
	}
	second, err := selector.Select(context.Background(), netx.Flow{}, []string{"b", "a"})
	if err != nil || second != "a" {
		t.Fatalf("wrapped round robin second=%q err=%v", second, err)
	}
}

func TestSelectorsRejectEmptyCandidates(t *testing.T) {
	for _, selector := range []netx.ConfigSelector{&roundRobinSelector{}, consistentHashSelector{}, newStickySelector(time.Now, time.Minute, 2)} {
		if _, err := selector.Select(context.Background(), netx.Flow{}, nil); err == nil {
			t.Fatal("empty candidate set unexpectedly succeeded")
		}
	}
}

func TestConsistentHashIgnoresCandidateOrderAndLimitsRemapping(t *testing.T) {
	selector := consistentHashSelector{}
	flows := make([]netx.Flow, 100)
	for i := range flows {
		flows[i] = netx.Flow{Network: "tcp", Host: "host-" + string(rune('a'+i%26)), Port: 443, Purpose: "user_traffic"}
	}
	for _, flow := range flows {
		one, err := selector.Select(context.Background(), flow, []string{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		two, err := selector.Select(context.Background(), flow, []string{"c", "a", "b"})
		if err != nil || one != two {
			t.Fatalf("order changed selection: %q vs %q, err=%v", one, two, err)
		}
	}
	changed := 0
	for _, flow := range flows {
		before, _ := selector.Select(context.Background(), flow, []string{"a", "b", "c"})
		after, _ := selector.Select(context.Background(), flow, []string{"a", "b", "c", "d"})
		if before != after {
			changed++
		}
	}
	if changed >= len(flows) {
		t.Fatalf("all keys remapped after adding a candidate: %d", changed)
	}
}

func TestConsistentHashUsesLengthDelimitedKeysAndStableFlowFallback(t *testing.T) {
	if selectionScore("a\x00b", "c") == selectionScore("a", "b\x00c") {
		t.Fatal("length-delimited hash inputs are ambiguous")
	}
	selector := consistentHashSelector{}
	one, err := selector.Select(context.Background(), netx.Flow{ID: "flow-1", Address: "example.com:443"}, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := selector.Select(context.Background(), netx.Flow{ID: "flow-2", Network: "tcp", Host: "example.com", Port: 443}, []string{"b", "a"})
	if err != nil || one != two {
		t.Fatalf("equivalent flow representations changed selection: %q vs %q err=%v", one, two, err)
	}
}

func TestStickySessionMembershipExpiryAndBounds(t *testing.T) {
	now := time.Unix(100, 0)
	selector := newStickySelector(func() time.Time { return now }, time.Second, 2)
	flow := netx.Flow{Network: "tcp", Host: "example.com", Port: 443, Tags: map[string]string{"session_key": "same"}}
	first, err := selector.Select(context.Background(), flow, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := selector.Select(context.Background(), flow, []string{"b", "a"})
	if first != second {
		t.Fatalf("sticky session changed candidate: %q -> %q", first, second)
	}
	removed, _ := selector.Select(context.Background(), flow, []string{"c"})
	if removed != "c" {
		t.Fatalf("removed candidate was retained: %q", removed)
	}
	now = now.Add(2 * time.Second)
	_, _ = selector.Select(context.Background(), netx.Flow{Tags: map[string]string{"session_key": "one"}}, []string{"a"})
	_, _ = selector.Select(context.Background(), netx.Flow{Tags: map[string]string{"session_key": "two"}}, []string{"b"})
	_, _ = selector.Select(context.Background(), netx.Flow{Tags: map[string]string{"session_key": "three"}}, []string{"c"})
	if len(selector.entries) > 2 {
		t.Fatalf("sticky map exceeded bound: %d", len(selector.entries))
	}
}

func TestStickySessionDoesNotCacheAnEmptyFlowKey(t *testing.T) {
	selector := newStickySelector(time.Now, time.Minute, 2)
	if _, err := selector.Select(context.Background(), netx.Flow{}, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if len(selector.entries) != 0 {
		t.Fatalf("empty flow key was cached: %#v", selector.entries)
	}
}

func TestStickySessionConcurrentAccessStaysBounded(t *testing.T) {
	selector := newStickySelector(time.Now, time.Minute, 8)
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 32; iteration++ {
				flow := netx.Flow{Network: "tcp", Host: "example.com", Port: 443, Tags: map[string]string{"session_key": string(rune('a' + (worker+iteration)%16))}}
				if _, err := selector.Select(context.Background(), flow, []string{"a", "b", "c"}); err != nil {
					t.Errorf("concurrent sticky select: %v", err)
				}
			}
		}(worker)
	}
	wg.Wait()
	if len(selector.entries) > 8 {
		t.Fatalf("sticky map exceeded concurrent bound: %d", len(selector.entries))
	}
}

func TestFactoryRejectsInvalidStrategy(t *testing.T) {
	if _, err := (Factory{}).Create(context.Background(), pluginContext(), []byte(`{"strategy":"random"}`)); err == nil {
		t.Fatal("invalid strategy unexpectedly accepted")
	}
}

// Keep the test independent from the registry; Factory only needs the context
// shape to parse selector configuration.
func pluginContext() plugin.InstanceContext { return plugin.InstanceContext{} }
