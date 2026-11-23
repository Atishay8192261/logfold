package reason

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Atishay8192261/logfold/internal/core"
)

type transcript struct {
	steps []core.Step
	n     int
}

func (b *transcript) call(name string, args map[string]any, output string) {
	id := fmt.Sprintf("c%d", b.n)
	b.n++
	b.steps = append(b.steps,
		core.Step{Kind: core.StepToolCall, ToolCall: &core.ToolCall{ID: id, Name: name, Args: args}},
		core.Step{Kind: core.StepObservation, Obs: &core.Observation{CallID: id, Output: output}},
	)
}

func TestHeuristicSurveysFirst(t *testing.T) {
	d, err := NewHeuristic().Next(context.Background(), Request{Goal: "web down"})
	if err != nil {
		t.Fatal(err)
	}
	if d.ToolCall == nil || d.ToolCall.Name != "list_dir" {
		t.Fatalf("first action should be list_dir, got %+v", d)
	}
}

func TestHeuristicInspectsConfigAfterSurvey(t *testing.T) {
	b := &transcript{}
	b.call("list_dir", map[string]any{"path": "."},
		"file         20  nginx.conf\nfile         40  app.log\ndir           0  sub\n")

	d, err := NewHeuristic().Next(context.Background(), Request{Goal: "web down", Steps: b.steps})
	if err != nil {
		t.Fatal(err)
	}
	if d.ToolCall == nil || d.ToolCall.Name != "read_file" || d.ToolCall.Args["path"] != "nginx.conf" {
		t.Fatalf("should read the config file next, got %+v", d)
	}
}

func TestHeuristicConcludesWithDiagnosis(t *testing.T) {
	b := &transcript{}
	b.call("list_dir", map[string]any{"path": "."}, "file  20  nginx.conf\nfile  40  app.log\n")
	b.call("read_file", map[string]any{"path": "nginx.conf"}, "listen 9090;\n")
	b.call("tail_log", map[string]any{"path": "app.log"}, "starting...\nERROR: connection refused on :8080\n")
	b.call("grep", map[string]any{"pattern": errKeywords, "path": "."}, "app.log:2: ERROR: connection refused on :8080")

	d, err := NewHeuristic().Next(context.Background(), Request{Goal: "web down", Steps: b.steps})
	if err != nil {
		t.Fatal(err)
	}
	if !d.IsFinal() {
		t.Fatalf("should conclude, got tool call %+v", d.ToolCall)
	}
	if !strings.Contains(d.Final, "Investigation summary") || !strings.Contains(strings.ToLower(d.Final), "refused") {
		t.Fatalf("summary missing evidence:\n%s", d.Final)
	}
	if !strings.Contains(d.Final, "not listening") {
		t.Fatalf("diagnosis missing for 'refused':\n%s", d.Final)
	}
}

func TestHeuristicRewritesPortNotIPOctet(t *testing.T) {
	b := &transcript{}
	b.call("list_dir", map[string]any{"path": "."}, "file  30  nginx.conf\n")
	b.call("read_file", map[string]any{"path": "nginx.conf"}, "listen 127.0.0.1:9090;\n")
	b.call("grep", map[string]any{"pattern": errKeywords, "path": "."}, "(no matches)")

	d, err := NewHeuristic().Next(context.Background(), Request{Goal: "fix the gateway to listen on port 8080", Steps: b.steps})
	if err != nil {
		t.Fatal(err)
	}
	if d.ToolCall == nil || d.ToolCall.Name != "write_file" {
		t.Fatalf("expected a write_file fix, got %+v", d)
	}
	content, _ := d.ToolCall.Args["content"].(string)
	if !strings.Contains(content, "127.0.0.1:8080") {
		t.Fatalf("port not rewritten correctly: %q", content)
	}
	if strings.Contains(content, "8080.0.0.1") {
		t.Fatalf("IP octet was corrupted: %q", content)
	}
}

func TestHeuristicDoesNotRewriteCorrectConfig(t *testing.T) {
	b := &transcript{}
	b.call("list_dir", map[string]any{"path": "."}, "file  30  nginx.conf\n")
	b.call("read_file", map[string]any{"path": "nginx.conf"}, "listen 127.0.0.1:8080;\n")
	b.call("grep", map[string]any{"pattern": errKeywords, "path": "."}, "(no matches)")

	d, _ := NewHeuristic().Next(context.Background(), Request{Goal: "fix the gateway to listen on port 8080", Steps: b.steps})
	if !d.IsFinal() {
		t.Fatalf("an already-correct config must not be rewritten, got tool call %+v", d.ToolCall)
	}
}

func TestExpectedPortIgnoresClockTimes(t *testing.T) {
	cases := map[string]string{
		"at 12:30 restart and fix port 8080": "8080",
		"the service on :8080 is down":       "8080",
		"restart the box at 12:30":           "", // a time is not a port
	}
	for goal, want := range cases {
		if got := expectedPort(goal); got != want {
			t.Fatalf("expectedPort(%q) = %q, want %q", goal, got, want)
		}
	}
}
