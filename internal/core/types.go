// Package core defines the fundamental data model shared across Praxis:
// the messages, tool calls, observations, steps, and the append-only event
// stream that every agent run is built from.
//
// Everything in Praxis is event-sourced: an agent run is nothing more than an
// ordered list of Events. The live run state is a pure fold over those events
// (see Replay), which is what makes deterministic replay possible.
package core

import (
	"encoding/json"
	"time"
)

// Role identifies who produced a Message in a conversation transcript.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single turn in the transcript handed to a Reasoner.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ToolCall is the agent's intent to invoke a named tool with arguments.
type ToolCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// Observation is the result of executing a ToolCall.
type Observation struct {
	CallID    string `json:"call_id"`
	Output    string `json:"output"`
	Error     string `json:"error,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

// Failed reports whether the tool call produced an error.
func (o Observation) Failed() bool { return o.Error != "" }

// StepKind enumerates the kinds of steps that make up a run transcript.
type StepKind string

const (
	StepPlan        StepKind = "plan"
	StepToolCall    StepKind = "tool_call"
	StepObservation StepKind = "observation"
	StepFinal       StepKind = "final"
	StepError       StepKind = "error"
)

// Step is one materialized entry in a run transcript, reconstructed from an
// Event during Replay.
type Step struct {
	Index    int          `json:"index"`
	Kind     StepKind     `json:"kind"`
	Thought  string       `json:"thought,omitempty"`
	ToolCall *ToolCall    `json:"tool_call,omitempty"`
	Obs      *Observation `json:"observation,omitempty"`
	Final    string       `json:"final,omitempty"`
	Err      string       `json:"error,omitempty"`
}

// RunStatus is the lifecycle state of an agent run.
type RunStatus string

const (
	StatusRunning RunStatus = "running"
	StatusSuccess RunStatus = "success"
	StatusFailed  RunStatus = "failed"
	StatusAborted RunStatus = "aborted"
)

// RunState is the current, fully-materialized view of a run. It is derived
// purely by folding the run's Events; it is never mutated directly by callers.
type RunState struct {
	ID     string    `json:"id"`
	Goal   string    `json:"goal"`
	Status RunStatus `json:"status"`
	Steps  []Step    `json:"steps"`
	Result string    `json:"result,omitempty"`
}

// Event kinds. Each corresponds to a payload type documented in state.go.
const (
	KindRunStarted  = "run_started"
	KindPlan        = "plan"
	KindToolCall    = "tool_call"
	KindObservation = "observation"
	KindFinal       = "final"
	KindError       = "error"
)

// Event is a single immutable entry in a run's append-only log. Data holds the
// kind-specific payload as raw JSON so the journal stays schema-agnostic and
// forward-compatible.
type Event struct {
	Seq  int             `json:"seq"`
	Time time.Time       `json:"time"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// Payload structs for the built-in event kinds.

type RunStartedData struct {
	ID   string `json:"id"`
	Goal string `json:"goal"`
}

type PlanData struct {
	Thought string `json:"thought"`
}

type FinalData struct {
	Result string `json:"result"`
}

type ErrorData struct {
	Error string `json:"error"`
}
