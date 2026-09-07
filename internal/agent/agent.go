// Package agent implements the ReAct control loop that turns a goal into
// action. Each iteration asks the Reasoner for the next step, records it in the
// journal, executes any tool call, records the observation, and repeats until
// the reasoner finishes or the step budget is exhausted.
//
// The agent never mutates run state directly: it only appends events. The live
// view and any later replay both come from folding those events, so a run is
// fully reproducible.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Atishay8192261/praxis/internal/core"
	"github.com/Atishay8192261/praxis/internal/journal"
	"github.com/Atishay8192261/praxis/internal/reason"
	"github.com/Atishay8192261/praxis/internal/tool"
)

// Agent wires a reasoner, a tool registry, and a journal into a runnable loop.
type Agent struct {
	Reasoner reason.Reasoner
	Tools    *tool.Registry
	Journal  *journal.Journal

	// MaxSteps bounds the loop. Zero means the default (12).
	MaxSteps int

	// OnEvent, if set, is called synchronously for every event appended, in
	// order. Used for live CLI/UI output.
	OnEvent func(core.Event)
}

// Options configures a new Agent.
type Options struct {
	MaxSteps int
}

// New constructs an Agent.
func New(r reason.Reasoner, tools *tool.Registry, j *journal.Journal, opts Options) *Agent {
	ms := opts.MaxSteps
	if ms <= 0 {
		ms = 12
	}
	return &Agent{Reasoner: r, Tools: tools, Journal: j, MaxSteps: ms}
}

var runSeq atomic.Uint64

func newRunID() string {
	return fmt.Sprintf("run-%d-%d", time.Now().Unix(), runSeq.Add(1))
}

// Run executes the agent loop against a goal and returns the final run state.
// It returns a non-nil error only for unexpected internal failures; ordinary
// agent failures (bad reasoning, tool errors, budget exhaustion) are recorded
// as run status and returned in the state.
func (a *Agent) Run(ctx context.Context, goal string) (*core.RunState, error) {
	id := newRunID()
	if err := a.emit(core.KindRunStarted, core.RunStartedData{ID: id, Goal: goal}); err != nil {
		return nil, err
	}

	seen := map[string]int{}

	for step := 0; step < a.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			_ = a.emit(core.KindError, core.ErrorData{Error: "cancelled: " + err.Error()})
			return a.Journal.State()
		}

		state, err := a.Journal.State()
		if err != nil {
			return nil, err
		}

		dec, err := a.Reasoner.Next(ctx, reason.Request{
			Goal:  goal,
			Steps: state.Steps,
			Tools: a.Tools.Specs(),
		})
		if err != nil {
			_ = a.emit(core.KindError, core.ErrorData{Error: "reasoner: " + err.Error()})
			return a.Journal.State()
		}

		if dec.Thought != "" {
			_ = a.emit(core.KindPlan, core.PlanData{Thought: dec.Thought})
		}

		if dec.IsFinal() {
			_ = a.emit(core.KindFinal, core.FinalData{Result: dec.Final})
			return a.Journal.State()
		}

		call := *dec.ToolCall
		call.ID = fmt.Sprintf("call-%d", step+1)

		// Loop guard: if the reasoner keeps proposing the same call, stop
		// rather than spin forever on an unattended run.
		sig := call.Name + "|" + argsSignature(call.Args)
		seen[sig]++
		if seen[sig] > 3 {
			_ = a.emit(core.KindError, core.ErrorData{
				Error: fmt.Sprintf("aborting: tool call %q repeated with no progress", call.Name),
			})
			return a.Journal.State()
		}

		if err := a.emit(core.KindToolCall, call); err != nil {
			return nil, err
		}
		obs := a.Tools.Dispatch(ctx, call)
		if err := a.emit(core.KindObservation, obs); err != nil {
			return nil, err
		}
	}

	_ = a.emit(core.KindError, core.ErrorData{
		Error: fmt.Sprintf("step budget (%d) exhausted before reaching a conclusion", a.MaxSteps),
	})
	return a.Journal.State()
}

func (a *Agent) emit(kind string, data any) error {
	ev, err := a.Journal.Append(kind, data)
	if err != nil {
		return err
	}
	if a.OnEvent != nil {
		a.OnEvent(ev)
	}
	return nil
}

// argsSignature produces a stable string for a set of args (encoding/json sorts
// map keys), used only for loop detection.
func argsSignature(args map[string]any) string {
	b, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("%v", args)
	}
	return string(b)
}
