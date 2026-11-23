package journal

import (
	"bytes"
	"testing"

	"github.com/Atishay8192261/logfold/internal/core"
)

// writeSampleRun appends a representative agent run to j and returns nothing;
// it exercises every built-in event kind.
func writeSampleRun(t *testing.T, j *Journal) {
	t.Helper()
	must := func(_ core.Event, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	must(j.Append(core.KindRunStarted, core.RunStartedData{ID: "run-1", Goal: "why is web down"}))
	must(j.Append(core.KindPlan, core.PlanData{Thought: "check the port first"}))
	must(j.Append(core.KindToolCall, core.ToolCall{ID: "c1", Name: "check_port", Args: map[string]any{"port": 8080}}))
	must(j.Append(core.KindObservation, core.Observation{CallID: "c1", Output: "closed", ElapsedMS: 3}))
	must(j.Append(core.KindFinal, core.FinalData{Result: "nginx not listening on 8080"}))
}

func TestAppendAndReplay(t *testing.T) {
	j := New(nil)
	writeSampleRun(t, j)

	st, err := j.State()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.ID != "run-1" || st.Goal != "why is web down" {
		t.Fatalf("unexpected run identity: %+v", st)
	}
	if st.Status != core.StatusSuccess {
		t.Fatalf("want success, got %q", st.Status)
	}
	if st.Result != "nginx not listening on 8080" {
		t.Fatalf("unexpected result: %q", st.Result)
	}
	if len(st.Steps) != 4 { // plan, tool_call, observation, final
		t.Fatalf("want 4 steps, got %d: %+v", len(st.Steps), st.Steps)
	}
	for i, s := range st.Steps {
		if s.Index != i {
			t.Fatalf("step %d has wrong index %d", i, s.Index)
		}
	}
}

// TestPersistReloadDeterminism is the core guarantee: persist a run, reload it
// from the JSONL bytes, and confirm the reconstructed state is identical.
func TestPersistReloadDeterminism(t *testing.T) {
	var buf bytes.Buffer
	j := New(&buf)
	writeSampleRun(t, j)

	live, err := j.State()
	if err != nil {
		t.Fatalf("live state: %v", err)
	}

	events, err := Load(&buf)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	reloaded, err := core.Replay(events)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	if live.ID != reloaded.ID || live.Goal != reloaded.Goal ||
		live.Status != reloaded.Status || live.Result != reloaded.Result ||
		len(live.Steps) != len(reloaded.Steps) {
		t.Fatalf("replay mismatch:\n live=%+v\n reloaded=%+v", live, reloaded)
	}
}

func TestLoadToleratesTruncatedFinalLine(t *testing.T) {
	var buf bytes.Buffer
	j := New(&buf)
	if _, err := j.Append(core.KindRunStarted, core.RunStartedData{ID: "r", Goal: "g"}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(core.KindPlan, core.PlanData{Thought: "t"}); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-append: a partial final line with no closing brace.
	buf.WriteString(`{"seq":2,"kind":"tool_ca`)

	events, err := Load(&buf)
	if err != nil {
		t.Fatalf("Load should tolerate a truncated final line: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want the 2 complete events, got %d", len(events))
	}
}

func TestLoadRejectsInteriorCorruption(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("{ this is not json }\n")
	buf.WriteString(`{"seq":1,"kind":"plan","data":{"thought":"x"}}` + "\n")
	if _, err := Load(&buf); err == nil {
		t.Fatal("a malformed interior line should be reported as corruption")
	}
}

func TestSubscribeReceivesLiveEvents(t *testing.T) {
	j := New(nil)
	_, _ = j.Append(core.KindRunStarted, core.RunStartedData{ID: "r", Goal: "g"})

	snapshot, live, cancel := j.Subscribe()
	defer cancel()
	if len(snapshot) != 1 {
		t.Fatalf("want 1 snapshot event, got %d", len(snapshot))
	}

	if _, err := j.Append(core.KindPlan, core.PlanData{Thought: "t"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	select {
	case ev := <-live:
		if ev.Kind != core.KindPlan {
			t.Fatalf("want plan event, got %q", ev.Kind)
		}
	default:
		t.Fatal("expected a live event on the subscription channel")
	}
}
