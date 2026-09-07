package tool

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Atishay8192261/praxis/internal/core"
)

// Tool is a capability the agent can invoke. Implementations must be safe for
// concurrent use.
type Tool interface {
	Name() string
	Description() string
	Schema() Schema
	Invoke(ctx context.Context, args Args) (string, error)
}

// Spec is the serializable description of a tool, used to tell the reasoner
// which tools exist and how to call them.
type Spec struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Params      []Param `json:"params"`
}

// Temporary lets a tool error opt into retry. An error whose Temporary()
// returns true is retried (with backoff) up to Registry.MaxAttempts.
type Temporary interface {
	Temporary() bool
}

// tempError is a convenience wrapper to mark an error retryable.
type tempError struct{ err error }

func (e tempError) Error() string   { return e.err.Error() }
func (e tempError) Unwrap() error   { return e.err }
func (e tempError) Temporary() bool { return true }

// Retryable marks err as safe to retry.
func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return tempError{err}
}

// Registry holds the set of tools available to an agent and dispatches calls.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool

	// MaxAttempts bounds retries for tools that return Temporary errors.
	MaxAttempts int
}

// NewRegistry returns an empty registry with a single attempt per call.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool), MaxAttempts: 1}
}

// Register adds or replaces a tool by name.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all tools sorted by name.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Specs returns serializable specs for all tools, sorted by name.
func (r *Registry) Specs() []Spec {
	tools := r.List()
	specs := make([]Spec, 0, len(tools))
	for _, t := range tools {
		specs = append(specs, Spec{
			Name:        t.Name(),
			Description: t.Description(),
			Params:      t.Schema().Params,
		})
	}
	return specs
}

// Dispatch validates and executes a tool call, returning an Observation. It
// never returns an error itself: failures are captured in Observation.Error so
// the agent can observe and recover from them.
func (r *Registry) Dispatch(ctx context.Context, call core.ToolCall) core.Observation {
	start := time.Now()
	obs := core.Observation{CallID: call.ID}

	t, ok := r.Get(call.Name)
	if !ok {
		obs.Error = fmt.Sprintf("unknown tool %q", call.Name)
		obs.ElapsedMS = msSince(start)
		return obs
	}

	args, err := t.Schema().Validate(call.Args)
	if err != nil {
		obs.Error = "invalid arguments: " + err.Error()
		obs.ElapsedMS = msSince(start)
		return obs
	}

	attempts := r.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var out string
	for i := 0; i < attempts; i++ {
		out, err = t.Invoke(ctx, args)
		if err == nil {
			break
		}
		te, isTemp := err.(Temporary)
		if !isTemp || !te.Temporary() || i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			i = attempts // stop
		case <-time.After(backoff(i)):
		}
	}

	obs.ElapsedMS = msSince(start)
	if err != nil {
		obs.Error = err.Error()
	} else {
		obs.Output = out
	}
	return obs
}

func msSince(t time.Time) int64 { return time.Since(t).Milliseconds() }

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt)) * 50 * time.Millisecond
	if d > time.Second {
		d = time.Second
	}
	return d
}
