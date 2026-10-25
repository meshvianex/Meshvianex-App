package health

import "sync"

type NetworkState string

const (
	HealthyNetwork    NetworkState = "healthy"
	DegradedNetwork   NetworkState = "degraded"
	UnstableNetwork   NetworkState = "unstable"
	RestrictedNetwork NetworkState = "restricted"
	LocalOnlyNetwork  NetworkState = "local_only"
	OfflineNetwork    NetworkState = "offline"
)

type Observation struct {
	DNS, TCP, UDP, TLS, International, Local bool
	LossPercent                              float64
	RTTMS                                    float64
}
type ConditionEngine struct {
	mu          sync.RWMutex
	state       NetworkState
	observation Observation
}

func NewConditionEngine() *ConditionEngine { return &ConditionEngine{state: OfflineNetwork} }
func (e *ConditionEngine) Update(o Observation) NetworkState {
	state := HealthyNetwork
	if !o.Local && !o.DNS && !o.TCP && !o.UDP {
		state = OfflineNetwork
	} else if o.Local && !o.International {
		state = LocalOnlyNetwork
	} else if !o.DNS || !o.TCP || !o.TLS {
		state = RestrictedNetwork
	} else if o.LossPercent >= 20 || o.RTTMS >= 1000 {
		state = UnstableNetwork
	} else if o.LossPercent >= 5 || o.RTTMS >= 300 {
		state = DegradedNetwork
	}
	e.mu.Lock()
	e.observation, e.state = o, state
	e.mu.Unlock()
	return state
}
func (e *ConditionEngine) State() NetworkState { e.mu.RLock(); defer e.mu.RUnlock(); return e.state }
func (e *ConditionEngine) Observation() Observation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.observation
}
