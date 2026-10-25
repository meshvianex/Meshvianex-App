package testing

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Test struct {
	ID      string
	Name    string
	Timeout time.Duration
	Run     func(context.Context) error
}
type Result struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Passed   bool          `json:"passed"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
}
type Provider interface{ Tests() []Test }
type SelfTester interface{ SelfTest(context.Context) error }

type Engine struct {
	mu    sync.RWMutex
	tests map[string]Test
}

func NewEngine() *Engine { return &Engine{tests: map[string]Test{}} }
func (e *Engine) Register(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("nil test provider")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range provider.Tests() {
		if t.ID == "" || t.Run == nil {
			return fmt.Errorf("test id and runner are required")
		}
		if _, ok := e.tests[t.ID]; ok {
			return fmt.Errorf("duplicate test %s", t.ID)
		}
		e.tests[t.ID] = t
	}
	return nil
}
func (e *Engine) List() []Test {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Test, 0, len(e.tests))
	for _, t := range e.tests {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (e *Engine) Run(ctx context.Context, id string) Result {
	e.mu.RLock()
	t, ok := e.tests[id]
	e.mu.RUnlock()
	if !ok {
		return Result{ID: id, Error: "test not found"}
	}
	started := time.Now()
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := t.Run(runCtx)
	result := Result{ID: t.ID, Name: t.Name, Passed: err == nil, Duration: time.Since(started)}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}
func (e *Engine) RunAll(ctx context.Context) []Result {
	tests := e.List()
	out := make([]Result, 0, len(tests))
	for _, t := range tests {
		out = append(out, e.Run(ctx, t.ID))
	}
	return out
}
func RunSelfTest(ctx context.Context, tester SelfTester, timeout time.Duration) error {
	if tester == nil {
		return fmt.Errorf("nil self tester")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return tester.SelfTest(c)
}
