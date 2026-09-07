// Package graph is a small, typed workflow engine — the "build your own
// LangGraph" core of Praxis. A Graph is a set of named nodes connected by
// edges. Edges may be conditional (branching), a node may fan out into work
// that runs concurrently and joins before proceeding (parallel fan-out/
// fan-in), and edges may point backwards to form bounded loops.
//
// Nodes communicate through a concurrency-safe State (a blackboard). The
// engine has zero third-party dependencies.
package graph

import (
	"context"
	"fmt"
	"sync"
)

const defaultMaxVisits = 1000

// State is a concurrency-safe blackboard shared by all nodes in a run.
type State struct {
	mu   sync.Mutex
	data map[string]any
}

// NewState returns an empty blackboard.
func NewState() *State { return &State{data: map[string]any{}} }

// Set stores a value.
func (s *State) Set(key string, v any) {
	s.mu.Lock()
	s.data[key] = v
	s.mu.Unlock()
}

// Get retrieves a value.
func (s *State) Get(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok
}

// String retrieves a value as a string ("" if absent or not a string).
func (s *State) String(key string) string {
	v, _ := s.Get(key)
	str, _ := v.(string)
	return str
}

// Bool retrieves a value as a bool (false if absent or not a bool).
func (s *State) Bool(key string) bool {
	v, _ := s.Get(key)
	b, _ := v.(bool)
	return b
}

// NodeFunc is the work a node performs. It reads and writes the shared State.
type NodeFunc func(ctx context.Context, s *State) error

type node struct {
	id       string
	run      NodeFunc
	parallel []NodeFunc
}

type edge struct {
	to   string
	cond func(*State) bool // nil means unconditional
}

// Graph is a workflow definition. Build it with the fluent methods, then Run.
type Graph struct {
	nodes map[string]*node
	edges map[string][]edge
	start string

	// MaxVisits bounds how many times any single node may execute, guarding
	// against runaway loops. Zero uses the default.
	MaxVisits int

	// Observer, if set, is called as each node starts and finishes, with phase
	// "start", "done", or "error". Used for live visualization.
	Observer func(nodeID, phase string)
}

// New returns an empty graph.
func New() *Graph {
	return &Graph{nodes: map[string]*node{}, edges: map[string][]edge{}, MaxVisits: defaultMaxVisits}
}

// Node adds a node that runs fn. The first node added becomes the start node.
func (g *Graph) Node(id string, fn NodeFunc) *Graph {
	g.nodes[id] = &node{id: id, run: fn}
	if g.start == "" {
		g.start = id
	}
	return g
}

// Parallel adds a node whose fns run concurrently and join before the graph
// proceeds (fan-out/fan-in).
func (g *Graph) Parallel(id string, fns ...NodeFunc) *Graph {
	g.nodes[id] = &node{id: id, parallel: fns}
	if g.start == "" {
		g.start = id
	}
	return g
}

// Start sets the entry node explicitly.
func (g *Graph) Start(id string) *Graph { g.start = id; return g }

// Edge adds an unconditional transition from -> to.
func (g *Graph) Edge(from, to string) *Graph {
	g.edges[from] = append(g.edges[from], edge{to: to})
	return g
}

// EdgeIf adds a conditional transition taken only when cond(state) is true.
// Conditional edges are evaluated in the order added; the first match wins.
func (g *Graph) EdgeIf(from, to string, cond func(*State) bool) *Graph {
	g.edges[from] = append(g.edges[from], edge{to: to, cond: cond})
	return g
}

// Run executes the graph starting at the start node, following edges until a
// node has no matching outgoing edge. It returns the ordered path of visited
// node ids.
func (g *Graph) Run(ctx context.Context, s *State) ([]string, error) {
	if s == nil {
		s = NewState()
	}
	if g.start == "" {
		return nil, fmt.Errorf("graph has no start node")
	}
	max := g.MaxVisits
	if max <= 0 {
		max = defaultMaxVisits
	}

	var path []string
	visits := map[string]int{}
	cur := g.start
	for cur != "" {
		n := g.nodes[cur]
		if n == nil {
			return path, fmt.Errorf("unknown node %q", cur)
		}
		visits[cur]++
		if visits[cur] > max {
			return path, fmt.Errorf("loop bound (%d) exceeded at node %q", max, cur)
		}
		if err := ctx.Err(); err != nil {
			return path, err
		}
		path = append(path, cur)

		g.observe(cur, "start")
		if err := g.exec(ctx, n, s); err != nil {
			g.observe(cur, "error")
			return path, fmt.Errorf("node %q: %w", cur, err)
		}
		g.observe(cur, "done")

		cur = g.next(cur, s)
	}
	return path, nil
}

func (g *Graph) exec(ctx context.Context, n *node, s *State) error {
	if len(n.parallel) > 0 {
		return runParallel(ctx, s, n.parallel)
	}
	if n.run != nil {
		return n.run(ctx, s)
	}
	return nil
}

func (g *Graph) next(from string, s *State) string {
	for _, e := range g.edges[from] {
		if e.cond == nil || e.cond(s) {
			return e.to
		}
	}
	return ""
}

func (g *Graph) observe(id, phase string) {
	if g.Observer != nil {
		g.Observer(id, phase)
	}
}

func runParallel(ctx context.Context, s *State, fns []NodeFunc) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, fn := range fns {
		wg.Add(1)
		go func(f NodeFunc) {
			defer wg.Done()
			if err := f(ctx, s); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(fn)
	}
	wg.Wait()
	return firstErr
}
