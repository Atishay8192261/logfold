package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Atishay8192261/praxis/internal/core"
	"github.com/Atishay8192261/praxis/internal/journal"
	"github.com/Atishay8192261/praxis/internal/reason"
	"github.com/Atishay8192261/praxis/internal/sandbox"
	"github.com/Atishay8192261/praxis/internal/tool"
	"github.com/Atishay8192261/praxis/internal/tools/devops"
)

// buildScenario writes a broken-service workspace: an nginx config on the wrong
// port and an application log that reports a connection-refused error.
func buildScenario(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nginx.conf", "server {\n  listen 9090;\n  root /srv/www;\n}\n")
	write("app.log", "boot ok\nupstream connect...\nERROR: connection refused on 127.0.0.1:8080\nretrying\n")
	write("README.md", "the web service should be reachable on :8080\n")
	return dir
}

func newAgent(t *testing.T, dir string) (*Agent, *journal.Journal) {
	t.Helper()
	sb, err := sandbox.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	devops.Register(reg, sb)
	j := journal.New(nil)
	return New(reason.NewHeuristic(), reg, j, Options{MaxSteps: 12}), j
}

func TestAgentDiagnosesBrokenService(t *testing.T) {
	dir := buildScenario(t)
	a, j := newAgent(t, dir)

	state, err := a.Run(context.Background(), "diagnose why the web service is down")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if state.Status != core.StatusSuccess {
		t.Fatalf("expected success, got %q; steps:\n%s", state.Status, dump(state))
	}
	low := strings.ToLower(state.Result)
	if !strings.Contains(low, "refused") || !strings.Contains(low, "listening") {
		t.Fatalf("diagnosis did not identify the connection issue:\n%s", state.Result)
	}

	// The agent must have actually inspected the config and the log.
	usedTools := map[string]bool{}
	for _, s := range state.Steps {
		if s.Kind == core.StepToolCall {
			usedTools[s.ToolCall.Name] = true
		}
	}
	for _, want := range []string{"list_dir", "read_file", "tail_log", "grep"} {
		if !usedTools[want] {
			t.Fatalf("agent never used %q; steps:\n%s", want, dump(state))
		}
	}

	// Replay determinism: folding the journal reproduces the same state.
	replayed, err := core.Replay(j.Events())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replayed.Status != state.Status || replayed.Result != state.Result || len(replayed.Steps) != len(state.Steps) {
		t.Fatal("replayed state diverged from live state")
	}
}

func TestAgentAppliesFix(t *testing.T) {
	dir := buildScenario(t)
	a, _ := newAgent(t, dir)

	state, err := a.Run(context.Background(), "fix the config so the gateway listens on port 8080")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if state.Status != core.StatusSuccess {
		t.Fatalf("expected success, got %q:\n%s", state.Status, dump(state))
	}

	got, err := os.ReadFile(filepath.Join(dir, "nginx.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "listen 8080;") {
		t.Fatalf("agent did not apply the port fix:\n%s", got)
	}
	if strings.Contains(string(got), "listen 9090;") {
		t.Fatalf("old port still present:\n%s", got)
	}

	wrote := false
	for _, s := range state.Steps {
		if s.Kind == core.StepToolCall && s.ToolCall.Name == "write_file" {
			wrote = true
		}
	}
	if !wrote {
		t.Fatal("agent never called write_file")
	}
}

func TestAgentDryRunDoesNotModify(t *testing.T) {
	dir := buildScenario(t)
	sb, err := sandbox.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	sb.DryRun = true
	reg := tool.NewRegistry()
	devops.Register(reg, sb)
	a := New(reason.NewHeuristic(), reg, journal.New(nil), Options{MaxSteps: 12})

	if _, err := a.Run(context.Background(), "fix the config so the gateway listens on port 8080"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "nginx.conf"))
	if !strings.Contains(string(got), "listen 9090;") {
		t.Fatalf("dry-run must not modify files:\n%s", got)
	}
}

func TestAgentEmitsEventsInOrder(t *testing.T) {
	dir := buildScenario(t)
	a, _ := newAgent(t, dir)

	var kinds []string
	a.OnEvent = func(ev core.Event) { kinds = append(kinds, ev.Kind) }

	if _, err := a.Run(context.Background(), "diagnose the outage"); err != nil {
		t.Fatal(err)
	}
	if len(kinds) == 0 || kinds[0] != core.KindRunStarted {
		t.Fatalf("first event should be run_started, got %v", kinds)
	}
	if last := kinds[len(kinds)-1]; last != core.KindFinal {
		t.Fatalf("last event should be final, got %q", last)
	}
}

func dump(s *core.RunState) string {
	var b strings.Builder
	for _, st := range s.Steps {
		switch st.Kind {
		case core.StepToolCall:
			b.WriteString("  call " + st.ToolCall.Name + "\n")
		case core.StepObservation:
			b.WriteString("  obs  " + firstLine(st.Obs.Output) + st.Obs.Error + "\n")
		case core.StepPlan:
			b.WriteString("  plan " + st.Thought + "\n")
		case core.StepFinal:
			b.WriteString("  final " + st.Final + "\n")
		case core.StepError:
			b.WriteString("  err  " + st.Err + "\n")
		}
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
