package health

import (
	"math"
	"sync"
	"time"
)

type State struct {
	Score          int       `json:"score"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
	LatencyMS      float64   `json:"latency_ms,omitempty"`
	LossPercent    float64   `json:"loss_percent,omitempty"`
	ThroughputKbps float64   `json:"throughput_kbps,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
	Stale          bool      `json:"stale,omitempty"`
}

type Metrics struct {
	DNSReachable   bool
	TCPReachable   bool
	UDPReachable   bool
	TLSReachable   bool
	LatencyMS      float64
	LossPercent    float64
	ThroughputKbps float64
}

type Store struct {
	mu         sync.RWMutex
	states     map[string]State
	staleAfter time.Duration
}

func NewStore() *Store { return &Store{states: map[string]State{}} }

func (s *Store) SetStaleAfter(after time.Duration) {
	s.mu.Lock()
	s.staleAfter = after
	s.mu.Unlock()
}
func (s *Store) Set(id string, score int) {
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	status := "healthy"
	if score < 30 {
		status = "failed"
	} else if score < 50 {
		status = "unstable"
	} else if score < 70 {
		status = "degraded"
	}
	s.mu.Lock()
	s.states[id] = State{Score: score, Status: status, UpdatedAt: time.Now().UTC()}
	s.mu.Unlock()
}

// SetMetrics converts probe observations into the normalized health state used
// by graph and realm policies. An unreachable TCP/TLS path is considered
// failed; the remaining observations refine the score.
func (s *Store) SetMetrics(id string, m Metrics) State {
	score := 100.0
	if !m.DNSReachable {
		score -= 20
	}
	if !m.TCPReachable {
		score -= 35
	}
	if !m.TLSReachable {
		score -= 20
	}
	if m.UDPReachable == false {
		score -= 5
	}
	if m.LossPercent > 0 {
		score -= math.Min(30, m.LossPercent*1.5)
	}
	if m.LatencyMS > 100 {
		score -= math.Min(20, (m.LatencyMS-100)/50)
	}
	if m.ThroughputKbps > 0 && m.ThroughputKbps < 128 {
		score -= 10
	}
	if score < 0 {
		score = 0
	}
	s.Set(id, int(score))
	s.mu.Lock()
	state := s.states[id]
	state.LatencyMS = m.LatencyMS
	state.LossPercent = m.LossPercent
	state.ThroughputKbps = m.ThroughputKbps
	s.states[id] = state
	s.mu.Unlock()
	return state
}
func (s *Store) Get(id string) State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.states[id]; ok {
		if s.staleAfter > 0 && !v.UpdatedAt.IsZero() && time.Since(v.UpdatedAt) > s.staleAfter {
			v.Stale = true
			v.Status = "stale"
			v.Score = 0
		}
		return v
	}
	return State{Score: 0, Status: "unknown"}
}

// RecordProbe stores a direct observation without manufacturing a healthy
// state when no probe has run.
func (s *Store) RecordProbe(id, status string, latencyMS float64, probeError string) State {
	score := 0
	switch status {
	case "healthy", "ok", "reachable":
		score = 100
	case "degraded":
		score = 60
	case "failed", "unreachable":
		score = 0
	default:
		status = "unknown"
	}
	if status == "ok" || status == "reachable" {
		status = "healthy"
	}
	s.mu.Lock()
	state := State{Score: score, Status: status, Error: probeError, LatencyMS: latencyMS, UpdatedAt: time.Now().UTC()}
	if probeError != "" {
		state.Status = "failed"
		state.Score = 0
	}
	s.states[id] = state
	s.mu.Unlock()
	return state
}

func (s *Store) MarkStale(id string) State {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.states[id]
	state.Status, state.Score, state.Stale, state.UpdatedAt = "stale", 0, true, time.Now().UTC()
	s.states[id] = state
	return state
}
func (s *Store) Snapshot() map[string]State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]State{}
	for k, v := range s.states {
		out[k] = v
	}
	return out
}
