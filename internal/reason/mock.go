package reason

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Atishay8192261/praxis/internal/core"
)

// Heuristic is a deterministic, model-free reasoner that performs generic
// DevOps triage: survey the workspace, inspect config files, tail logs, grep
// for error indicators, then summarize the findings and likely cause.
//
// It is intentionally scenario-agnostic — it reasons from what it observes, not
// from a hardcoded script — so it works on any workspace and makes the whole
// system usable and testable without a model.
type Heuristic struct{}

// NewHeuristic returns the model-free triage reasoner.
func NewHeuristic() *Heuristic { return &Heuristic{} }

func (h *Heuristic) Name() string { return "heuristic" }

var errRe = regexp.MustCompile(errKeywords)

func (h *Heuristic) Next(_ context.Context, req Request) (Decision, error) {
	v := analyze(req.Steps)

	// 1. Survey the workspace.
	if !v.listed {
		return act("Survey the workspace to see what is here.",
			"list_dir", map[string]any{"path": "."}), nil
	}

	// 2. Inspect configuration files.
	for _, f := range v.files {
		if isConfig(f) && !v.read[f] {
			return act(fmt.Sprintf("Inspect the configuration file %q.", f),
				"read_file", map[string]any{"path": f}), nil
		}
	}

	// 3. Tail logs for recent errors.
	for _, f := range v.files {
		if isLog(f) && !v.tailed[f] {
			return act(fmt.Sprintf("Check %q for recent errors.", f),
				"tail_log", map[string]any{"path": f, "lines": int64(40)}), nil
		}
	}

	// 4. Grep the whole workspace for error indicators.
	if !v.grepped {
		return act("Search the workspace for error indicators.",
			"grep", map[string]any{"pattern": errKeywords, "path": "."}), nil
	}

	// 5. If the goal asks for a fix and we found a concrete one, apply it.
	if wantsFix(req.Goal) {
		if f, nc := v.proposeFix(req.Goal); f != "" && !v.wrote[f] {
			return act(fmt.Sprintf("Apply the fix: correct the port in %q.", f),
				"write_file", map[string]any{"path": f, "content": nc}), nil
		}
	}

	// 6. Conclude.
	return Decision{Thought: "Enough evidence gathered; summarizing findings.", Final: v.summary(req.Goal)}, nil
}

func act(thought, name string, args map[string]any) Decision {
	return Decision{Thought: thought, ToolCall: &core.ToolCall{Name: name, Args: args}}
}

// view is the reconstructed state of what the agent has learned so far.
type view struct {
	listed     bool
	files      []string
	read       map[string]bool
	tailed     map[string]bool
	grepped    bool
	inspected  []string
	errorLines []string
	configs    map[string]string // config file path -> contents
	wrote      map[string]bool   // config files the agent has attempted to edit
	fixedOK    map[string]bool   // edits that actually succeeded
}

func analyze(steps []core.Step) view {
	v := view{read: map[string]bool{}, tailed: map[string]bool{}, configs: map[string]string{}, wrote: map[string]bool{}, fixedOK: map[string]bool{}}
	calls := map[string]*core.ToolCall{}
	for i := range steps {
		if steps[i].Kind == core.StepToolCall && steps[i].ToolCall != nil {
			calls[steps[i].ToolCall.ID] = steps[i].ToolCall
		}
	}
	for i := range steps {
		s := steps[i]
		if s.Kind != core.StepObservation || s.Obs == nil {
			continue
		}
		call := calls[s.Obs.CallID]
		if call == nil {
			continue
		}
		switch call.Name {
		case "list_dir":
			v.listed = true
			if !s.Obs.Failed() {
				v.files = append(v.files, parseListing(s.Obs.Output)...)
			}
		case "read_file":
			p, _ := call.Args["path"].(string)
			v.read[p] = true
			v.inspected = append(v.inspected, p)
			if isConfig(p) && !s.Obs.Failed() {
				v.configs[p] = s.Obs.Output
			}
			collectErrors(&v, s.Obs.Output)
		case "tail_log":
			p, _ := call.Args["path"].(string)
			v.tailed[p] = true
			v.inspected = append(v.inspected, p)
			collectErrors(&v, s.Obs.Output)
		case "grep":
			v.grepped = true
			if !s.Obs.Failed() && !strings.HasPrefix(strings.TrimSpace(s.Obs.Output), "(no matches)") {
				collectErrors(&v, s.Obs.Output)
			}
		case "write_file":
			p, _ := call.Args["path"].(string)
			// Mark attempted regardless of success so a failing write is not
			// re-proposed forever; track success separately for the summary.
			v.wrote[p] = true
			if !s.Obs.Failed() {
				v.fixedOK[p] = true
			}
		}
	}
	return v
}

func collectErrors(v *view, out string) {
	for _, line := range strings.Split(out, "\n") {
		if errRe.MatchString(line) {
			line = strings.TrimSpace(line)
			if line != "" && !contains(v.errorLines, line) {
				v.errorLines = append(v.errorLines, line)
			}
		}
	}
}

// parseListing extracts file names (not directories) from list_dir output,
// whose lines look like: "file       128  nginx.conf".
func parseListing(out string) []string {
	var files []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "file" {
			continue
		}
		name := strings.Join(fields[2:], " ")
		files = append(files, name)
	}
	return files
}

func (v view) summary(goal string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Investigation summary for: %s\n\n", goal)
	if len(v.inspected) > 0 {
		fmt.Fprintf(&b, "Inspected: %s\n", strings.Join(dedupe(v.inspected), ", "))
	}

	if len(v.errorLines) > 0 {
		b.WriteString("\nError indicators found:\n")
		for _, l := range capLines(v.errorLines, 8) {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
		fmt.Fprintf(&b, "\nLikely cause: %s\n", diagnose(v.errorLines))
	} else {
		b.WriteString("\nNo obvious error indicators were found in configs or logs.\n")
	}

	// Concrete, high-confidence finding: a configured port that disagrees with
	// the port the goal is asking about.
	if fix := v.portMismatch(goal); fix != "" {
		b.WriteString("\n" + fix + "\n")
	}

	if len(v.fixedOK) > 0 {
		files := make([]string, 0, len(v.fixedOK))
		for f := range v.fixedOK {
			files = append(files, f)
		}
		sort.Strings(files)
		fmt.Fprintf(&b, "\nFix applied: rewrote %s to the expected port. Restart the service and re-check the health endpoint to confirm.\n", strings.Join(files, ", "))
	}
	var failed []string
	for f := range v.wrote {
		if !v.fixedOK[f] {
			failed = append(failed, f)
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		fmt.Fprintf(&b, "\nAttempted to edit %s but the write failed (check permissions).\n", strings.Join(failed, ", "))
	}

	return strings.TrimRight(b.String(), "\n")
}

// wantsFix reports whether the goal is asking the agent to change something,
// not merely to diagnose.
func wantsFix(goal string) bool {
	g := strings.ToLower(goal)
	for _, k := range []string{"fix", "repair", "resolve", "correct", "make it listen", "set the port", "change the port", "update the config"} {
		if strings.Contains(g, k) {
			return true
		}
	}
	return false
}

// proposeFix returns a config file path and its corrected contents when a
// port mismatch can be repaired, or empty strings otherwise.
func (v view) proposeFix(goal string) (path, content string) {
	expected := expectedPort(goal)
	if expected == "" {
		return "", ""
	}
	for _, f := range dedupe(v.inspected) {
		c, ok := v.configs[f]
		if !ok {
			continue
		}
		nc, wrong, changed := rewritePort(c, expected)
		if changed && wrong != "" {
			return f, nc
		}
	}
	return "", ""
}

// rewritePort rewrites listen/port directives whose port differs from expected,
// returning the new content, the first wrong port it saw, and whether anything
// changed. Only the port digits on matching lines are touched.
func rewritePort(content, expected string) (string, string, bool) {
	lines := strings.Split(content, "\n")
	wrong := ""
	changed := false
	for i, ln := range lines {
		loc := portRe.FindStringSubmatchIndex(ln)
		if loc == nil {
			continue
		}
		p := ln[loc[2]:loc[3]]
		if p == expected {
			continue
		}
		if wrong == "" {
			wrong = p
		}
		lines[i] = ln[:loc[2]] + expected + ln[loc[3]:]
		changed = true
	}
	return strings.Join(lines, "\n"), wrong, changed
}

var (
	// portRe captures the actual port from a config directive, consuming an
	// optional "host:" (e.g. 127.0.0.1:) so an IP octet is never mistaken for
	// the port. Group 1 is the port.
	portRe = regexp.MustCompile(`(?i)(?:listen|port)\s*[=:]?\s*(?:[0-9]{1,3}(?:\.[0-9]{1,3}){3}:)?([0-9]{2,5})`)
	// A goal's expected port: prefer an explicit "port <n>"; only fall back to
	// a bare ":<n>" for 4-5 digit numbers so clock times (12:30) aren't caught.
	goalPortKeywordRe = regexp.MustCompile(`(?i)port[\s=:#]*([0-9]{2,5})`)
	goalPortColonRe   = regexp.MustCompile(`:([0-9]{4,5})\b`)
)

func expectedPort(goal string) string {
	if m := goalPortKeywordRe.FindStringSubmatch(goal); m != nil {
		return m[1]
	}
	if m := goalPortColonRe.FindStringSubmatch(goal); m != nil {
		return m[1]
	}
	return ""
}

// portMismatch looks for a configured port that differs from the port the goal
// mentions, and returns a concrete recommended fix if it finds one.
func (v view) portMismatch(goal string) string {
	expected := expectedPort(goal)
	if expected == "" {
		return ""
	}
	// Iterate configs in inspection order for deterministic output.
	for _, f := range dedupe(v.inspected) {
		content, ok := v.configs[f]
		if !ok {
			continue
		}
		for _, pm := range portRe.FindAllStringSubmatch(content, -1) {
			if pm[1] != expected {
				return fmt.Sprintf(
					"Root cause: %s is configured for port %s, but the goal expects port %s.\n"+
						"Recommended fix: update %s to use port %s (change %s -> %s) and restart the service.",
					f, pm[1], expected, f, expected, pm[1], expected)
			}
		}
	}
	return ""
}

func capLines(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	out := append([]string{}, xs[:n]...)
	return append(out, fmt.Sprintf("... and %d more", len(xs)-n))
}

func diagnose(lines []string) string {
	joined := strings.ToLower(strings.Join(lines, "\n"))
	switch {
	case strings.Contains(joined, "address already in use"):
		return "another process is already bound to the port; stop it or change the configured port"
	case strings.Contains(joined, "refused"):
		return "the service is not listening on the expected address/port (wrong or unstarted service)"
	case strings.Contains(joined, "denied") || strings.Contains(joined, "permission"):
		return "a permissions problem is blocking the service"
	case strings.Contains(joined, "no such file") || strings.Contains(joined, "not found"):
		return "a required file or path referenced by the config is missing"
	default:
		return "review the error indicators above; the failure is reported in the logs"
	}
}

func isConfig(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".conf", ".cfg", ".yaml", ".yml", ".ini", ".env", ".json", ".toml", ".properties"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return lower == "dockerfile" || strings.Contains(lower, "config")
}

func isLog(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".log") || strings.Contains(lower, "log")
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
