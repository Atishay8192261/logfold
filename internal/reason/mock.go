package reason

import (
	"context"
	"fmt"
	"regexp"
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

	// 5. Conclude.
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
}

func analyze(steps []core.Step) view {
	v := view{read: map[string]bool{}, tailed: map[string]bool{}}
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
		for _, l := range v.errorLines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
		fmt.Fprintf(&b, "\nLikely cause: %s\n", diagnose(v.errorLines))
	} else {
		b.WriteString("\nNo obvious error indicators were found in configs or logs.\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
