// Package playbook contains deterministic, graph-driven workflows built on the
// Praxis graph engine and tools. Unlike the ReAct agent (which asks a reasoner
// what to do next), a playbook is a fixed graph — useful when you want fast,
// repeatable, model-free automation.
//
// Scan is the flagship playbook: a parallel workspace triage that surveys a
// directory, inspects configs and logs concurrently (fan-out/fan-in), searches
// for error indicators, and reports.
package playbook

import (
	"context"
	"fmt"
	"strings"

	"github.com/Atishay8192261/praxis/internal/core"
	"github.com/Atishay8192261/praxis/internal/graph"
	"github.com/Atishay8192261/praxis/internal/tool"
	"github.com/Atishay8192261/praxis/internal/triage"
)

// RunScan builds and runs the parallel scan playbook against the tools in reg
// (which must include the DevOps toolset). It returns the report text and the
// ordered path of graph nodes visited. observer may be nil.
func RunScan(ctx context.Context, reg *tool.Registry, observer func(nodeID, phase string)) (string, []string, error) {
	dispatch := func(name string, args map[string]any) core.Observation {
		return reg.Dispatch(ctx, core.ToolCall{ID: name, Name: name, Args: args})
	}

	g := graph.New()
	g.Observer = observer

	g.Node("survey", func(_ context.Context, s *graph.State) error {
		obs := dispatch("list_dir", map[string]any{"path": "."})
		if obs.Failed() {
			return fmt.Errorf("list_dir: %s", obs.Error)
		}
		s.Set("files", parseFiles(obs.Output))
		return nil
	})

	// Inspect configs and logs concurrently.
	g.Parallel("inspect",
		func(_ context.Context, s *graph.State) error {
			var b strings.Builder
			for _, f := range stateFiles(s) {
				if isConfig(f) {
					obs := dispatch("read_file", map[string]any{"path": f})
					fmt.Fprintf(&b, "### %s\n%s\n\n", f, obs.Output)
				}
			}
			s.Set("configs", b.String())
			return nil
		},
		func(_ context.Context, s *graph.State) error {
			var b strings.Builder
			for _, f := range stateFiles(s) {
				if isLog(f) {
					obs := dispatch("tail_log", map[string]any{"path": f, "lines": int64(30)})
					fmt.Fprintf(&b, "### %s\n%s\n\n", f, obs.Output)
				}
			}
			s.Set("logs", b.String())
			return nil
		},
	)

	g.Node("scan_errors", func(_ context.Context, s *graph.State) error {
		obs := dispatch("grep", map[string]any{"pattern": triage.ErrorPattern, "path": "."})
		s.Set("errors", obs.Output)
		return nil
	})

	g.Node("report", func(_ context.Context, s *graph.State) error {
		s.Set("report", buildReport(s))
		return nil
	})

	g.Edge("survey", "inspect").
		Edge("inspect", "scan_errors").
		Edge("scan_errors", "report")

	st := graph.NewState()
	path, err := g.Run(ctx, st)
	if err != nil {
		return "", path, err
	}
	return st.String("report"), path, nil
}

func buildReport(s *graph.State) string {
	var b strings.Builder
	files := stateFiles(s)
	fmt.Fprintf(&b, "Workspace scan report\n\nFiles (%d): %s\n", len(files), strings.Join(files, ", "))

	errs := strings.TrimSpace(s.String("errors"))
	if errs == "" || strings.HasPrefix(errs, "(no matches)") {
		b.WriteString("\nNo error indicators found.\n")
	} else {
		lines := strings.Split(errs, "\n")
		b.WriteString("\nError indicators:\n")
		for i, l := range lines {
			if i >= 8 {
				fmt.Fprintf(&b, "  ... and %d more\n", len(lines)-8)
				break
			}
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func stateFiles(s *graph.State) []string {
	v, _ := s.Get("files")
	files, _ := v.([]string)
	return files
}

func parseFiles(listOutput string) []string {
	var files []string
	for _, line := range strings.Split(listOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "file" {
			continue
		}
		files = append(files, strings.Join(fields[2:], " "))
	}
	return files
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
