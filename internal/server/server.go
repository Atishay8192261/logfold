// Package server is the LogFold web workbench backend: a standard-library HTTP
// server exposing a small REST API plus a Server-Sent Events stream of live
// agent steps, and serving the embedded single-page UI. No web framework.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Atishay8192261/logfold/internal/agent"
	"github.com/Atishay8192261/logfold/internal/core"
	"github.com/Atishay8192261/logfold/internal/journal"
	"github.com/Atishay8192261/logfold/internal/memory"
	"github.com/Atishay8192261/logfold/internal/playbook"
	"github.com/Atishay8192261/logfold/internal/reason"
	"github.com/Atishay8192261/logfold/internal/sandbox"
	"github.com/Atishay8192261/logfold/internal/tool"
	"github.com/Atishay8192261/logfold/internal/tools/devops"
	"github.com/Atishay8192261/logfold/internal/tools/mem"
)

//go:embed webui
var uiFS embed.FS

// Options configures a Server.
type Options struct {
	Workspace  string // sandbox root for agent runs
	Backend    string // auto|heuristic|model
	MemoryPath string // optional long-term memory file
}

// Server holds workbench state and routes.
type Server struct {
	mux       *http.ServeMux
	workspace string
	backend   string
	memory    *memory.Store
	memPath   string

	mu   sync.Mutex
	runs map[string]*runRec
}

type runRec struct {
	ID      string
	Goal    string
	Journal *journal.Journal
	Started time.Time
}

// New builds a Server, loading memory if a path is configured.
func New(opts Options) (*Server, error) {
	if opts.Workspace == "" {
		opts.Workspace = "."
	}
	if opts.Backend == "" {
		opts.Backend = "auto"
	}
	s := &Server{
		workspace: opts.Workspace,
		backend:   opts.Backend,
		memPath:   opts.MemoryPath,
		runs:      map[string]*runRec{},
	}
	if opts.MemoryPath != "" {
		store, err := memory.LoadStore(opts.MemoryPath, 0)
		if err != nil {
			return nil, err
		}
		s.memory = store
	}
	s.routes()
	return s, nil
}

// Workspace returns the configured sandbox root.
func (s *Server) Workspace() string { return s.workspace }

func (s *Server) routes() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /api/tools", s.handleTools)
	mux.HandleFunc("POST /api/run", s.handleStartRun)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	mux.HandleFunc("GET /api/runs/{id}/events", s.handleRunEvents)
	mux.HandleFunc("POST /api/scan", s.handleScan)
	mux.HandleFunc("GET /api/memory", s.handleMemorySearch)
	mux.HandleFunc("POST /api/memory", s.handleMemoryAdd)

	sub, _ := fs.Sub(uiFS, "webui")
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	s.mux = mux
}

// Handler exposes the router (useful for tests).
func (s *Server) Handler() http.Handler { return s.mux }

// ListenAndServe starts the workbench on addr.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) newRegistry(sb *sandbox.Sandbox) *tool.Registry {
	reg := tool.NewRegistry()
	reg.MaxAttempts = 2
	devops.Register(reg, sb)
	if s.memory != nil {
		mem.Register(reg, s.memory)
	}
	return reg
}

func selectBackend(name string, envURL, envModel, envKey string) reason.Reasoner {
	switch name {
	case "heuristic":
		return reason.NewHeuristic()
	case "model":
		return reason.NewHTTPModel(envURL, envModel, envKey)
	default:
		return reason.FromEnv()
	}
}

// --- handlers ---

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	rb := selectBackend(s.backend, envURL(), envModel(), envKey())
	writeJSON(w, 200, map[string]any{
		"workspace": s.workspace,
		"backend":   rb.Name(),
		"memory":    s.memory != nil,
	})
}

func (s *Server) handleTools(w http.ResponseWriter, _ *http.Request) {
	sb, err := sandbox.New(s.workspace)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	writeJSON(w, 200, s.newRegistry(sb).Specs())
}

type startReq struct {
	Goal     string   `json:"goal"`
	DryRun   bool     `json:"dry_run"`
	MaxSteps int      `json:"max_steps"`
	Backend  string   `json:"backend"`
	Allow    []string `json:"allow"`
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var req startReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.Goal == "" {
		httpError(w, 400, fmt.Errorf("goal is required"))
		return
	}

	sb, err := sandbox.New(s.workspace)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	sb.DryRun = req.DryRun
	sb.Allow = req.Allow

	reg := s.newRegistry(sb)
	j := journal.New(nil)
	id := newRunID()
	rec := &runRec{ID: id, Goal: req.Goal, Journal: j, Started: time.Now()}

	s.mu.Lock()
	s.runs[id] = rec
	s.mu.Unlock()

	backend := req.Backend
	if backend == "" {
		backend = s.backend
	}
	r0 := selectBackend(backend, envURL(), envModel(), envKey())
	ms := req.MaxSteps
	if ms <= 0 {
		ms = 12
	}
	ag := agent.New(r0, reg, j, agent.Options{MaxSteps: ms})
	ag.Memory = s.memory

	go func() {
		_, _ = ag.Run(context.Background(), req.Goal)
		if s.memory != nil && s.memPath != "" {
			_ = s.memory.Save(s.memPath)
		}
	}()

	writeJSON(w, 200, map[string]string{"run_id": id, "backend": r0.Name()})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	rec := s.run(r.PathValue("id"))
	if rec == nil {
		httpError(w, 404, fmt.Errorf("run not found"))
		return
	}
	state, err := rec.Journal.State()
	if err != nil {
		httpError(w, 500, err)
		return
	}
	writeJSON(w, 200, state)
}

type runSummary struct {
	ID     string         `json:"id"`
	Goal   string         `json:"goal"`
	Status core.RunStatus `json:"status"`
	Steps  int            `json:"steps"`
}

func (s *Server) handleListRuns(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	recs := make([]*runRec, 0, len(s.runs))
	for _, r := range s.runs {
		recs = append(recs, r)
	}
	s.mu.Unlock()

	out := make([]runSummary, 0, len(recs))
	for _, rec := range recs {
		st, _ := rec.Journal.State()
		sum := runSummary{ID: rec.ID, Goal: rec.Goal}
		if st != nil {
			sum.Status = st.Status
			sum.Steps = len(st.Steps)
		}
		out = append(out, sum)
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	rec := s.run(r.PathValue("id"))
	if rec == nil {
		httpError(w, 404, fmt.Errorf("run not found"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, 500, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	snapshot, live, cancel := rec.Journal.Subscribe()
	defer cancel()

	for _, ev := range snapshot {
		writeSSE(w, ev)
		if terminal(ev.Kind) {
			flusher.Flush()
			writeSSEEnd(w)
			flusher.Flush()
			return
		}
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case ev, ok := <-live:
			if !ok {
				return
			}
			writeSSE(w, ev)
			flusher.Flush()
			if terminal(ev.Kind) {
				writeSSEEnd(w)
				flusher.Flush()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	sb, err := sandbox.New(s.workspace)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	reg := s.newRegistry(sb)
	report, path, err := playbook.RunScan(r.Context(), reg, nil)
	if err != nil {
		httpError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"report": report, "graph": path})
}

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	if s.memory == nil {
		writeJSON(w, 200, []memory.Hit{})
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, 200, s.memory.Records())
		return
	}
	writeJSON(w, 200, s.memory.Search(q, 10))
}

func (s *Server) handleMemoryAdd(w http.ResponseWriter, r *http.Request) {
	if s.memory == nil {
		httpError(w, 400, fmt.Errorf("memory is not enabled (start with -memory)"))
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		httpError(w, 400, fmt.Errorf("text is required"))
		return
	}
	id := s.memory.Add(req.Text, nil)
	if s.memPath != "" {
		_ = s.memory.Save(s.memPath)
	}
	writeJSON(w, 200, map[string]int{"id": id})
}

// --- helpers ---

func (s *Server) run(id string) *runRec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id]
}

var runCounter atomic.Uint64

func newRunID() string {
	return fmt.Sprintf("run-%d-%d", time.Now().Unix(), runCounter.Add(1))
}

func envURL() string   { return os.Getenv("PRAXIS_MODEL_URL") }
func envModel() string { return os.Getenv("PRAXIS_MODEL_NAME") }
func envKey() string   { return os.Getenv("PRAXIS_MODEL_KEY") }

func terminal(kind string) bool { return kind == core.KindFinal || kind == core.KindError }

func writeSSE(w http.ResponseWriter, ev core.Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func writeSSEEnd(w http.ResponseWriter) {
	fmt.Fprint(w, "event: end\ndata: {}\n\n")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
