package logscan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Atishay8192261/logfold/internal/triage"
)

const (
	MaxLine         = 64 * 1024
	MaxGroups       = 2048
	MaxBuckets      = 72
	MaxGroupSources = 8
)

type Report struct {
	Schema      int      `json:"schema"`
	HasBaseline bool     `json:"hasBaseline"`
	Incident    Counts   `json:"incident"`
	Baseline    Counts   `json:"baseline"`
	Sources     []Source `json:"sources"`
	Groups      []Group  `json:"groups"`
	Timeline    Timeline `json:"timeline"`
}

type Counts struct {
	Lines    int64 `json:"lines"`
	Events   int64 `json:"events"`
	Skipped  int64 `json:"skipped"`
	Untimed  int64 `json:"untimed"`
	Overflow int64 `json:"overflow"`
	Redacted int64 `json:"redacted"`
}

type Source struct {
	Name     string `json:"name"`
	Baseline bool   `json:"baseline"`
	Lines    int64  `json:"lines"`
}

type Sample struct {
	Source int    `json:"source"`
	Line   int64  `json:"line"`
	Text   string `json:"text"`
	Time   string `json:"time,omitempty"`
}

type Occurrences struct {
	Source int   `json:"source"`
	Count  int64 `json:"count"`
}

type Window struct {
	Count             int64         `json:"count"`
	Rate              float64       `json:"ratePer1000"`
	First             string        `json:"first,omitempty"`
	Last              string        `json:"last,omitempty"`
	Sample            *Sample       `json:"sample,omitempty"`
	Sources           []Occurrences `json:"sources,omitempty"`
	OtherSourceEvents int64         `json:"otherSourceEvents,omitempty"`
	first, last       time.Time
}

type Group struct {
	ID       string `json:"id"`
	Level    string `json:"level"`
	Label    string `json:"label"`
	Change   string `json:"change"`
	Incident Window `json:"incident"`
	Baseline Window `json:"baseline"`
}

type Scan struct {
	sources            []Source
	groups             map[[32]byte]*Group
	incident, baseline Counts
	series             histogram
	hasBaseline        bool
	active             bool
	pending            []byte
	oversized, inKey   bool
}

func New() *Scan {
	return &Scan{groups: make(map[[32]byte]*Group), series: histogram{width: 60}}
}

func (s *Scan) Start(name string, baseline bool) error {
	if s.active {
		return errors.New("previous log source is still open")
	}
	name, _ = triage.Redact(name)
	s.sources = append(s.sources, Source{Name: clip(name, 200), Baseline: baseline})
	s.hasBaseline = s.hasBaseline || baseline
	s.active = true
	return nil
}

func (s *Scan) Write(chunk []byte) (int, error) {
	if !s.active {
		return 0, errors.New("no log source is open")
	}
	n := len(chunk)
	for len(chunk) > 0 {
		line, rest, complete := bytes.Cut(chunk, []byte{'\n'})
		switch {
		case s.oversized || len(s.pending)+len(line) > MaxLine:
			s.pending = s.pending[:0]
			s.oversized = true
		case complete && len(s.pending) == 0:
			s.process(line)
			chunk = rest
			continue
		default:
			s.pending = append(s.pending, line...)
		}
		if !complete {
			break
		}
		s.finishLine()
		chunk = rest
	}
	return n, nil
}

func (s *Scan) End() {
	if !s.active {
		return
	}
	if s.oversized || len(s.pending) != 0 {
		s.finishLine()
	}
	s.inKey = false
	s.active = false
}

func (s *Scan) finishLine() {
	if s.oversized {
		s.source().Lines++
		s.counts().Skipped++
	} else {
		s.process(s.pending)
	}
	s.pending = s.pending[:0]
	s.oversized = false
}

func (s *Scan) source() *Source { return &s.sources[len(s.sources)-1] }

func (s *Scan) counts() *Counts {
	if s.source().Baseline {
		return &s.baseline
	}
	return &s.incident
}

func (s *Scan) process(raw []byte) {
	source, counts := s.source(), s.counts()
	source.Lines++
	raw = bytes.TrimSpace(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}))
	if len(raw) == 0 {
		return
	}
	if !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) {
		counts.Skipped++
		return
	}
	line := string(raw)
	if strings.Contains(line, "-----BEGIN ") && strings.Contains(line, "PRIVATE KEY-----") {
		s.inKey = true
	}
	if s.inKey {
		counts.Redacted++
		if strings.Contains(line, "-----END ") && strings.Contains(line, "PRIVATE KEY-----") {
			s.inKey = false
		}
		return
	}
	counts.Lines++
	parsed := parseEvent(line)
	switch parsed.level {
	case "info", "debug":
		return
	case "":
		if !textSignal.MatchString(parsed.message) {
			return
		}
		parsed.level = "signal"
	}
	if parsed.message == "" {
		parsed.message = "(no message)"
	}
	message, masked := triage.Redact(parsed.message)
	counts.Redacted += int64(masked)
	label := canonical(message)
	id := sha256.Sum256([]byte(parsed.level + "\x00" + label))
	counts.Events++
	if parsed.at.IsZero() {
		counts.Untimed++
	} else if !source.Baseline {
		s.series.add(parsed.at.Unix())
	}
	group := s.groups[id]
	if group == nil {
		if len(s.groups) == MaxGroups {
			counts.Overflow++
			return
		}
		group = &Group{ID: hex.EncodeToString(id[:16]), Level: parsed.level, Label: clip(label, 320)}
		s.groups[id] = group
	}
	window := &group.Incident
	if source.Baseline {
		window = &group.Baseline
	}
	window.record(parsed.at, len(s.sources), source.Lines, message)
}

func (w *Window) record(at time.Time, source int, line int64, text string) {
	w.Count++
	index := slices.IndexFunc(w.Sources, func(item Occurrences) bool { return item.Source == source })
	switch {
	case index >= 0:
		w.Sources[index].Count++
	case len(w.Sources) < MaxGroupSources:
		w.Sources = append(w.Sources, Occurrences{source, 1})
	default:
		w.OtherSourceEvents++
	}
	if w.Sample == nil || !at.Before(w.last) {
		w.Sample = &Sample{Source: source, Line: line, Text: clip(text, 500), Time: iso(at)}
	}
	if !at.IsZero() {
		if w.first.IsZero() || at.Before(w.first) {
			w.first = at
		}
		if at.After(w.last) {
			w.last = at
		}
	}
}

func (s *Scan) Report() Report {
	report := Report{
		Schema: 1, HasBaseline: s.hasBaseline, Incident: s.incident, Baseline: s.baseline,
		Sources: slices.Clone(s.sources), Groups: make([]Group, 0, len(s.groups)), Timeline: s.series.report(),
	}
	for _, original := range s.groups {
		group := *original
		group.Incident.summarize(s.incident.Lines)
		group.Baseline.summarize(s.baseline.Lines)
		group.Change = change(group, s.baseline.Lines > 0)
		report.Groups = append(report.Groups, group)
	}
	slices.SortFunc(report.Groups, func(a, b Group) int {
		if a.Incident.Count != b.Incident.Count {
			if a.Incident.Count > b.Incident.Count {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return report
}

func (w *Window) summarize(total int64) {
	if total > 0 {
		w.Rate = float64(w.Count) * 1000 / float64(total)
	}
	w.First, w.Last = iso(w.first), iso(w.last)
	w.Sources = slices.Clone(w.Sources)
}

func change(g Group, compare bool) string {
	switch {
	case !compare:
		return "observed"
	case g.Incident.Count == 0:
		return "baseline only"
	case g.Baseline.Count == 0:
		return "new"
	case g.Incident.Rate > g.Baseline.Rate:
		return "increased"
	case g.Incident.Rate < g.Baseline.Rate:
		return "decreased"
	default:
		return "unchanged"
	}
}

func clip(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return strings.Clone(s[:maxBytes]) + "…"
}

func iso(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}
