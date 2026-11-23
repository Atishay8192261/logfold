package reason

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Atishay8192261/logfold/internal/tool"
)

func TestParseDecision(t *testing.T) {
	// Action form.
	d := parseDecision(`{"thought":"look around","action":{"tool":"read_file","args":{"path":"a.conf"}}}`)
	if d.ToolCall == nil || d.ToolCall.Name != "read_file" || d.ToolCall.Args["path"] != "a.conf" {
		t.Fatalf("action parse failed: %+v", d)
	}

	// Final form, wrapped in prose + code fence (as chatty models emit).
	d = parseDecision("Sure, here you go:\n```json\n{\"thought\":\"done\",\"final\":\"nginx port was wrong\"}\n```\nHope that helps!")
	if !d.IsFinal() || d.Final != "nginx port was wrong" {
		t.Fatalf("fenced final parse failed: %+v", d)
	}

	// Braces inside strings must not confuse the extractor.
	d = parseDecision(`{"thought":"the config has {braces}","final":"ok"}`)
	if !d.IsFinal() || d.Final != "ok" {
		t.Fatalf("string-brace parse failed: %+v", d)
	}

	// No JSON at all -> treat as final answer.
	d = parseDecision("I could not determine the cause.")
	if !d.IsFinal() || d.Final != "I could not determine the cause." {
		t.Fatalf("plain-text fallback failed: %+v", d)
	}
}

func TestParseDecisionPrefersRealOverDecoyExample(t *testing.T) {
	// The model restates the format as an example, then gives its real action.
	content := `Reply like {"thought":"...","final":"..."}. My answer: {"thought":"looking","action":{"tool":"read_file","args":{"path":"a.conf"}}}`
	d := parseDecision(content)
	if d.ToolCall == nil || d.ToolCall.Name != "read_file" {
		t.Fatalf("decoy example won over the real action: %+v", d)
	}
}

func TestParseDecisionSurvivesStrayBrace(t *testing.T) {
	// A stray '{' in prose before the real object must not defeat extraction.
	content := "The config has a { character. Final answer:\n{\"final\":\"the port was wrong\"}"
	d := parseDecision(content)
	if !d.IsFinal() || d.Final != "the port was wrong" {
		t.Fatalf("stray brace defeated extraction: %+v", d)
	}
}

func TestHTTPModelNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		content := `{"thought":"survey","action":{"tool":"list_dir","args":{"path":"."}}}`
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, content)
	}))
	defer srv.Close()

	m := NewHTTPModel(srv.URL, "test-model", "")
	d, err := m.Next(context.Background(), Request{
		Goal:  "why is the web service down",
		Tools: []tool.Spec{{Name: "list_dir", Description: "list a dir"}},
	})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if d.ToolCall == nil || d.ToolCall.Name != "list_dir" {
		t.Fatalf("expected list_dir tool call, got %+v", d)
	}
}

func TestHTTPModelSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"model not loaded"}}`)
	}))
	defer srv.Close()

	m := NewHTTPModel(srv.URL, "test-model", "")
	if _, err := m.Next(context.Background(), Request{Goal: "x"}); err == nil {
		t.Fatal("expected error to be surfaced")
	}
}
