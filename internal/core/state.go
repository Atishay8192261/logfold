package core

import (
	"encoding/json"
	"fmt"
)

// Apply folds a single Event into a RunState. This is the one and only place
// that knows how an event mutates run state, which keeps live execution and
// after-the-fact replay perfectly consistent: both call Apply.
func Apply(s *RunState, ev Event) error {
	switch ev.Kind {
	case KindRunStarted:
		var d RunStartedData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return fmt.Errorf("decode run_started: %w", err)
		}
		s.ID = d.ID
		s.Goal = d.Goal
		s.Status = StatusRunning

	case KindPlan:
		var d PlanData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return fmt.Errorf("decode plan: %w", err)
		}
		s.append(Step{Kind: StepPlan, Thought: d.Thought})

	case KindToolCall:
		var tc ToolCall
		if err := json.Unmarshal(ev.Data, &tc); err != nil {
			return fmt.Errorf("decode tool_call: %w", err)
		}
		s.append(Step{Kind: StepToolCall, ToolCall: &tc})

	case KindObservation:
		var o Observation
		if err := json.Unmarshal(ev.Data, &o); err != nil {
			return fmt.Errorf("decode observation: %w", err)
		}
		s.append(Step{Kind: StepObservation, Obs: &o})

	case KindFinal:
		var d FinalData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return fmt.Errorf("decode final: %w", err)
		}
		s.Result = d.Result
		s.Status = StatusSuccess
		s.append(Step{Kind: StepFinal, Final: d.Result})

	case KindError:
		var d ErrorData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return fmt.Errorf("decode error: %w", err)
		}
		s.Status = StatusFailed
		s.append(Step{Kind: StepError, Err: d.Error})

	default:
		return fmt.Errorf("unknown event kind %q", ev.Kind)
	}
	return nil
}

func (s *RunState) append(step Step) {
	step.Index = len(s.Steps)
	s.Steps = append(s.Steps, step)
}

// Replay reconstructs the full RunState from an ordered slice of events. Given
// the same events it always yields the same state — the core determinism
// guarantee that makes LogFold runs debuggable.
func Replay(events []Event) (*RunState, error) {
	s := &RunState{}
	for _, ev := range events {
		if err := Apply(s, ev); err != nil {
			return nil, fmt.Errorf("replay event seq=%d: %w", ev.Seq, err)
		}
	}
	return s, nil
}
