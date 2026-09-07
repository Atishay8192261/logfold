// Command praxis is the CLI entrypoint for the Praxis agent runtime.
//
//	praxis run "<goal>"   run the DevOps agent headlessly on a goal
//	praxis serve          launch the web workbench
//	praxis version        print the version
//	praxis help           show usage
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Atishay8192261/praxis/internal/agent"
	"github.com/Atishay8192261/praxis/internal/core"
	"github.com/Atishay8192261/praxis/internal/journal"
	"github.com/Atishay8192261/praxis/internal/memory"
	"github.com/Atishay8192261/praxis/internal/playbook"
	"github.com/Atishay8192261/praxis/internal/reason"
	"github.com/Atishay8192261/praxis/internal/sandbox"
	"github.com/Atishay8192261/praxis/internal/tool"
	"github.com/Atishay8192261/praxis/internal/tools/devops"
	"github.com/Atishay8192261/praxis/internal/tools/mem"
)

const version = "0.1.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage(os.Stdout)
		return
	}
	switch args[0] {
	case "run":
		runCommand(args[1:])
	case "serve":
		serveCommand(args[1:])
	case "memory":
		memoryCommand(args[1:])
	case "scan":
		scanCommand(args[1:])
	case "version", "--version", "-v":
		fmt.Printf("praxis %s\n", version)
	case "help", "--help", "-h":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "praxis: unknown command %q\n\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func runCommand(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	workspace := fs.String("C", ".", "workspace directory (sandbox root)")
	maxSteps := fs.Int("max-steps", 12, "maximum agent steps")
	dryRun := fs.Bool("dry-run", false, "describe mutating actions instead of performing them")
	allow := fs.String("allow", "", "comma-separated commands run_cmd may execute")
	journalPath := fs.String("journal", "", "write the run journal (JSONL) to this path")
	backend := fs.String("backend", "auto", "reasoner backend: auto|heuristic|model")
	memPath := fs.String("memory", "", "long-term memory file to enable recall across runs")
	quiet := fs.Bool("quiet", false, "only print the final result")
	fs.Parse(args)

	goal := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if goal == "" {
		fmt.Fprintln(os.Stderr, "praxis run: a goal is required, e.g. praxis run \"why is the web service down\"")
		os.Exit(2)
	}

	sb, err := sandbox.New(*workspace)
	if err != nil {
		fatal("workspace: %v", err)
	}
	sb.DryRun = *dryRun
	if *allow != "" {
		sb.Allow = splitCSV(*allow)
	}

	reg := tool.NewRegistry()
	reg.MaxAttempts = 2
	devops.Register(reg, sb)

	var memStore *memory.Store
	if *memPath != "" {
		memStore, err = memory.LoadStore(*memPath, 0)
		if err != nil {
			fatal("memory: %v", err)
		}
		mem.Register(reg, memStore)
	}

	r := selectBackend(*backend)

	var sink *os.File
	if *journalPath != "" {
		sink, err = os.Create(*journalPath)
		if err != nil {
			fatal("journal: %v", err)
		}
		defer sink.Close()
	}
	var j *journal.Journal
	if sink != nil {
		j = journal.New(sink)
	} else {
		j = journal.New(nil)
	}

	ag := agent.New(r, reg, j, agent.Options{MaxSteps: *maxSteps})
	ag.Memory = memStore
	if !*quiet {
		ag.OnEvent = printEvent
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if !*quiet {
		fmt.Printf("praxis %s\n", version)
		fmt.Printf("goal:      %s\n", goal)
		fmt.Printf("workspace: %s\n", sb.Root)
		fmt.Printf("backend:   %s\n", r.Name())
		if *dryRun {
			fmt.Printf("mode:      dry-run (no changes will be applied)\n")
		}
		fmt.Println(strings.Repeat("-", 60))
	}

	start := time.Now()
	state, err := ag.Run(ctx, goal)
	if err != nil {
		fatal("run: %v", err)
	}

	if memStore != nil {
		if err := memStore.Save(*memPath); err != nil {
			fmt.Fprintf(os.Stderr, "praxis: warning: could not save memory: %v\n", err)
		}
	}

	if *quiet {
		fmt.Println(state.Result)
	} else {
		fmt.Println(strings.Repeat("-", 60))
		fmt.Printf("status: %s   steps: %d   elapsed: %s\n",
			state.Status, len(state.Steps), time.Since(start).Round(time.Millisecond))
		if *journalPath != "" {
			fmt.Printf("journal written to %s (replay with any JSONL reader)\n", *journalPath)
		}
	}

	if state.Status != core.StatusSuccess {
		os.Exit(1)
	}
}

func scanCommand(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	workspace := fs.String("C", ".", "workspace directory (sandbox root)")
	quiet := fs.Bool("quiet", false, "print only the report")
	fs.Parse(args)

	sb, err := sandbox.New(*workspace)
	if err != nil {
		fatal("workspace: %v", err)
	}
	reg := tool.NewRegistry()
	reg.MaxAttempts = 2
	devops.Register(reg, sb)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	observer := func(id, phase string) {
		if !*quiet && phase == "start" {
			fmt.Printf("▸ %s\n", id)
		}
	}

	report, path, err := playbook.RunScan(ctx, reg, observer)
	if err != nil {
		fatal("scan: %v", err)
	}
	if !*quiet {
		fmt.Println(strings.Repeat("-", 60))
	}
	fmt.Println(report)
	if !*quiet {
		fmt.Printf("\ngraph: %s\n", strings.Join(path, " -> "))
	}
}

func memoryCommand(args []string) {
	if len(args) == 0 {
		fatal("usage: praxis memory <add|search> [flags] \"<text>\"")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("memory", flag.ExitOnError)
	path := fs.String("memory", "praxis-memory.json", "memory file path")
	k := fs.Int("k", 5, "number of results for search")
	fs.Parse(rest)

	store, err := memory.LoadStore(*path, 0)
	if err != nil {
		fatal("memory: %v", err)
	}

	switch sub {
	case "add":
		text := strings.TrimSpace(strings.Join(fs.Args(), " "))
		if text == "" {
			fatal("usage: praxis memory add \"<text>\"")
		}
		id := store.Add(text, nil)
		if err := store.Save(*path); err != nil {
			fatal("save: %v", err)
		}
		fmt.Printf("stored memory #%d in %s\n", id, *path)
	case "search":
		q := strings.TrimSpace(strings.Join(fs.Args(), " "))
		if q == "" {
			fatal("usage: praxis memory search \"<query>\"")
		}
		hits := store.Search(q, *k)
		if len(hits) == 0 {
			fmt.Println("(no memories)")
			return
		}
		for _, h := range hits {
			fmt.Printf("[%.3f] %s\n", h.Score, h.Record.Text)
		}
	default:
		fatal("unknown memory subcommand %q (use add|search)", sub)
	}
}

func serveCommand(_ []string) {
	// Implemented in the HTTP-server milestone.
	fmt.Fprintln(os.Stderr, "praxis serve: the web workbench is not wired up yet in this build")
	os.Exit(2)
}

func selectBackend(name string) reason.Reasoner {
	switch name {
	case "heuristic":
		return reason.NewHeuristic()
	case "model":
		return reason.NewHTTPModel(os.Getenv("PRAXIS_MODEL_URL"), os.Getenv("PRAXIS_MODEL_NAME"), os.Getenv("PRAXIS_MODEL_KEY"))
	default:
		return reason.FromEnv()
	}
}

// printEvent renders a live event to stdout in a compact, readable form.
func printEvent(ev core.Event) {
	switch ev.Kind {
	case core.KindPlan:
		var d core.PlanData
		_ = json.Unmarshal(ev.Data, &d)
		fmt.Printf("\n▸ think  %s\n", d.Thought)
	case core.KindToolCall:
		var c core.ToolCall
		_ = json.Unmarshal(ev.Data, &c)
		args, _ := json.Marshal(c.Args)
		fmt.Printf("  → tool   %s %s\n", c.Name, string(args))
	case core.KindObservation:
		var o core.Observation
		_ = json.Unmarshal(ev.Data, &o)
		out := o.Output
		if o.Failed() {
			out = "ERROR: " + o.Error
		}
		fmt.Printf("  ← obs    %s  (%dms)\n", clip(out, 400), o.ElapsedMS)
	case core.KindFinal:
		var d core.FinalData
		_ = json.Unmarshal(ev.Data, &d)
		fmt.Printf("\n✔ result\n%s\n", d.Result)
	case core.KindError:
		var d core.ErrorData
		_ = json.Unmarshal(ev.Data, &d)
		fmt.Printf("\n✖ error  %s\n", d.Error)
	}
}

func clip(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " · "))
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "praxis: "+format+"\n", a...)
	os.Exit(1)
}

func usage(w *os.File) {
	fmt.Fprintf(w, `praxis %s - a from-scratch AI-agent orchestration runtime

Usage:
  praxis <command> [arguments]

Commands:
  run "<goal>"          run the DevOps agent on a goal (headless)
  scan                  fast parallel, model-free workspace triage (graph playbook)
  memory add "<text>"   store a note in long-term memory
  memory search "<q>"   search long-term memory
  serve                 launch the web workbench (default :7777)
  version               print version
  help                  show this help

run flags:
  -C <dir>        workspace directory / sandbox root (default ".")
  -max-steps <n>  maximum agent steps (default 12)
  -dry-run        describe mutating actions instead of performing them
  -allow <list>   comma-separated commands run_cmd may execute
  -journal <path> write the run journal (JSONL) for replay
  -memory <path>  long-term memory file to recall/store across runs
  -backend <b>    auto|heuristic|model (default auto)
  -quiet          print only the final result

Environment (model backend):
  PRAXIS_MODEL_URL   OpenAI-compatible base URL (e.g. http://127.0.0.1:11434/v1)
  PRAXIS_MODEL_NAME  model identifier
  PRAXIS_MODEL_KEY   optional bearer token

Praxis has zero third-party dependencies. With no model configured it uses a
deterministic heuristic backend, so it runs out of the box.
`, version)
}
