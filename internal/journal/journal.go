// Package journal implements the append-only event log at the heart of Praxis.
//
// A Journal records every event an agent run produces, in order, and can:
//   - persist events as newline-delimited JSON (JSONL) to any io.Writer,
//   - stream live events to subscribers (used by the web workbench for
//     real-time traces),
//   - be reloaded from disk and folded back into run state (deterministic
//     replay, via core.Replay).
//
// It is safe for concurrent use.
package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Atishay8192261/praxis/internal/core"
)

// clock is swappable so tests can make timestamps deterministic.
var clock = time.Now

// Journal is a concurrency-safe, append-only log of core.Events.
type Journal struct {
	mu     sync.Mutex
	seq    int
	events []core.Event
	sink   io.Writer // optional durable sink; events are written as JSONL
	subs   []chan core.Event
}

// New creates a Journal. If sink is non-nil, every appended event is also
// written to it as a single JSON line (JSONL), giving durable, replayable runs.
func New(sink io.Writer) *Journal {
	return &Journal{sink: sink}
}

// Append records a new event of the given kind with the given payload. The
// payload is marshaled to JSON immediately, so callers may reuse or mutate it
// afterwards. The fully-populated event (with sequence number and timestamp)
// is returned.
func (j *Journal) Append(kind string, payload any) (core.Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return core.Event{}, fmt.Errorf("marshal %s payload: %w", kind, err)
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	ev := core.Event{
		Seq:  j.seq,
		Time: clock().UTC(),
		Kind: kind,
		Data: data,
	}
	j.seq++
	j.events = append(j.events, ev)

	if j.sink != nil {
		line, err := json.Marshal(ev)
		if err != nil {
			return core.Event{}, fmt.Errorf("marshal event: %w", err)
		}
		if _, err := j.sink.Write(append(line, '\n')); err != nil {
			return core.Event{}, fmt.Errorf("write event: %w", err)
		}
	}

	// Fan out to live subscribers. Never block the writer: if a subscriber's
	// buffer is full it simply misses the live event (it can still catch up
	// via Snapshot/replay).
	for _, ch := range j.subs {
		select {
		case ch <- ev:
		default:
		}
	}

	return ev, nil
}

// Events returns a copy of all events recorded so far.
func (j *Journal) Events() []core.Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]core.Event, len(j.events))
	copy(out, j.events)
	return out
}

// State folds all recorded events into the current run state.
func (j *Journal) State() (*core.RunState, error) {
	return core.Replay(j.Events())
}

// Subscribe returns a channel that receives every event appended after the
// call, plus a cancel func that must be called to release the subscription.
// The returned slice is a point-in-time snapshot of events already recorded,
// so a late subscriber can render history then follow live updates without
// gaps or duplicates.
func (j *Journal) Subscribe() (snapshot []core.Event, live <-chan core.Event, cancel func()) {
	ch := make(chan core.Event, 256)

	j.mu.Lock()
	snapshot = make([]core.Event, len(j.events))
	copy(snapshot, j.events)
	j.subs = append(j.subs, ch)
	j.mu.Unlock()

	var once sync.Once
	cancel = func() {
		once.Do(func() {
			j.mu.Lock()
			defer j.mu.Unlock()
			for i, c := range j.subs {
				if c == ch {
					j.subs = append(j.subs[:i], j.subs[i+1:]...)
					close(ch)
					break
				}
			}
		})
	}
	return snapshot, ch, cancel
}

// Load reads a JSONL stream (as written by a sink) back into an event slice.
func Load(r io.Reader) ([]core.Event, error) {
	var events []core.Event
	sc := bufio.NewScanner(r)
	// Journal lines can be large (tool output); grow the buffer generously.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var ev core.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("parse journal line %d: %w", line, err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	return events, nil
}
