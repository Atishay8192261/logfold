package graph

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSequentialAndConditionalBranch(t *testing.T) {
	g := New()
	g.Node("survey", func(_ context.Context, s *State) error {
		s.Set("needFix", true)
		return nil
	})
	g.Node("diagnose", func(_ context.Context, s *State) error {
		s.Set("cause", "port mismatch")
		return nil
	})
	g.Node("fix", func(_ context.Context, s *State) error {
		s.Set("fixed", true)
		return nil
	})
	g.Node("done", func(_ context.Context, s *State) error { return nil })

	g.Edge("survey", "diagnose")
	g.EdgeIf("diagnose", "fix", func(s *State) bool { return s.Bool("needFix") })
	g.EdgeIf("diagnose", "done", func(s *State) bool { return !s.Bool("needFix") })
	g.Edge("fix", "done")

	s := NewState()
	path, err := g.Run(context.Background(), s)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.Join(path, ">"); got != "survey>diagnose>fix>done" {
		t.Fatalf("path = %s", got)
	}
	if !s.Bool("fixed") {
		t.Fatal("fix node did not run")
	}
}

func TestConditionalSkipsBranch(t *testing.T) {
	g := New()
	g.Node("a", func(_ context.Context, s *State) error { s.Set("needFix", false); return nil })
	g.Node("fix", func(_ context.Context, s *State) error { s.Set("fixed", true); return nil })
	g.Node("done", func(_ context.Context, _ *State) error { return nil })
	g.EdgeIf("a", "fix", func(s *State) bool { return s.Bool("needFix") })
	g.EdgeIf("a", "done", func(s *State) bool { return !s.Bool("needFix") })
	g.Edge("fix", "done")

	s := NewState()
	path, err := g.Run(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(path, ">") != "a>done" {
		t.Fatalf("expected fix to be skipped, path = %v", path)
	}
	if s.Bool("fixed") {
		t.Fatal("fix should not have run")
	}
}

// TestParallelIsConcurrent proves fan-out actually runs concurrently: both
// branches must reach a barrier before either is allowed to proceed. If they
// ran sequentially, the second signal would never arrive and the test times
// out.
func TestParallelIsConcurrent(t *testing.T) {
	reached := make(chan struct{}, 2)
	proceed := make(chan struct{})
	work := func(_ context.Context, _ *State) error {
		reached <- struct{}{}
		<-proceed
		return nil
	}

	g := New()
	g.Parallel("fanout", work, work)

	done := make(chan error, 1)
	go func() {
		_, err := g.Run(context.Background(), NewState())
		done <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-reached:
		case <-time.After(2 * time.Second):
			t.Fatal("parallel branches did not run concurrently")
		}
	}
	close(proceed)
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestParallelJoinsResults(t *testing.T) {
	var counter int64
	inc := func(_ context.Context, s *State) error {
		atomic.AddInt64(&counter, 1)
		return nil
	}
	g := New()
	g.Parallel("p", inc, inc, inc)
	g.Node("after", func(_ context.Context, s *State) error {
		s.Set("count", atomic.LoadInt64(&counter))
		return nil
	})
	g.Edge("p", "after")

	s := NewState()
	if _, err := g.Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get("count"); v.(int64) != 3 {
		t.Fatalf("expected all 3 parallel fns to join before 'after', got %v", v)
	}
}

func TestLoopBound(t *testing.T) {
	g := New()
	g.MaxVisits = 5
	g.Node("spin", func(_ context.Context, _ *State) error { return nil })
	g.Edge("spin", "spin") // infinite loop
	_, err := g.Run(context.Background(), NewState())
	if err == nil || !strings.Contains(err.Error(), "loop bound") {
		t.Fatalf("expected loop-bound error, got %v", err)
	}
}
