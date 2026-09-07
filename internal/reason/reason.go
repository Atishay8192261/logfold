// Package reason is the "brain" boundary of Praxis. A Reasoner looks at the
// goal, the transcript so far, and the available tools, and decides the next
// action: call a tool, or finish with an answer.
//
// The reasoning model is never a hard dependency. This package ships two
// backends:
//
//   - Heuristic: a deterministic, model-free DevOps triage policy. The whole
//     system builds, tests, and demos with no model at all.
//   - HTTPModel: an OpenAI-compatible client for a locally-run open-weights
//     model, using a from-scratch JSON action protocol (not the vendor's
//     function-calling). The model is an external service behind this
//     interface — swap it without touching the engine.
package reason

import (
	"context"
	"os"

	"github.com/Atishay8192261/praxis/internal/core"
	"github.com/Atishay8192261/praxis/internal/tool"
)

// Decision is a single step the reasoner wants the agent to take. Exactly one
// of ToolCall or Final is meaningful: if ToolCall is non-nil the agent invokes
// it; otherwise the run finishes with Final.
type Decision struct {
	Thought  string
	ToolCall *core.ToolCall
	Final    string
}

// IsFinal reports whether this decision ends the run.
func (d Decision) IsFinal() bool { return d.ToolCall == nil }

// Request is everything the reasoner needs to choose the next action.
type Request struct {
	Goal  string
	Steps []core.Step
	Tools []tool.Spec
}

// Reasoner decides what an agent does next.
type Reasoner interface {
	Name() string
	Next(ctx context.Context, req Request) (Decision, error)
}

// errKeywords is the shared pattern for "something went wrong" indicators, used
// by the heuristic backend and worth surfacing to the model too.
const errKeywords = `(?i)(error|fail(ed|ure)?|refused|denied|panic|exception|fatal|cannot|unable|not found|address already in use|no such file|timed out)`

// FromEnv selects a backend from the environment. If PRAXIS_MODEL_URL is set,
// it uses the HTTP model backend; otherwise it falls back to the deterministic
// heuristic so Praxis always runs out of the box.
//
//	PRAXIS_MODEL_URL   e.g. http://127.0.0.1:11434/v1
//	PRAXIS_MODEL_NAME  e.g. llama3.1  (model identifier)
//	PRAXIS_MODEL_KEY   optional bearer token
func FromEnv() Reasoner {
	if url := os.Getenv("PRAXIS_MODEL_URL"); url != "" {
		return NewHTTPModel(url, os.Getenv("PRAXIS_MODEL_NAME"), os.Getenv("PRAXIS_MODEL_KEY"))
	}
	return NewHeuristic()
}
