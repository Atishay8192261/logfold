# Praxis

**A from-scratch AI-agent orchestration runtime — with zero third-party dependencies.**

[![CI](https://github.com/Atishay8192261/praxis/actions/workflows/ci.yml/badge.svg)](https://github.com/Atishay8192261/praxis/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/go-1.23-00ADD8)
![deps](https://img.shields.io/badge/dependencies-0-brightgreen)
![license](https://img.shields.io/badge/license-MIT-blue)

Praxis is the layer that agent frameworks like LangGraph, CrewAI, and AutoGen
*sell* — a graph executor, a ReAct planning loop, a function-calling tool
protocol, vector-backed memory, and deterministic replay — except every line is
built on the Go **standard library**. No orchestration framework, no vector DB,
no HTTP framework, no SDKs. `go.mod` has an empty `require` block, on purpose.

On top of the runtime, Praxis ships a real product: a **DevOps automation
agent** and a **web workbench**. Point it at a broken service and a goal
("diagnose why the web service is unreachable on port 8080"); it plans, inspects
files / configs / logs / ports with sandboxed tools, and **diagnoses or fixes**
the problem — while a live UI streams every `plan → tool-call → observation`
step.

> **The model is not a dependency, it is a service.** Praxis talks to a
> reasoning model over HTTP through a `Reasoner` interface you can swap. It also
> ships a deterministic, model-free backend, so the entire system builds, tests,
> and *works* with no model at all.

---

## See it work (no model required)

```console
$ praxis run -C examples/broken-web "diagnose why the web service is unreachable on port 8080"
▸ think  Survey the workspace to see what is here.
  → tool   list_dir {"path":"."}
  ← obs    file  README.md · file  app.log · file  nginx.conf   (0ms)
▸ think  Inspect the configuration file "nginx.conf".
  → tool   read_file {"path":"nginx.conf"}
  ← obs    server { listen 9090; ... location /health { return 200 "ok"; } }   (0ms)
▸ think  Check "app.log" for recent errors.
  → tool   tail_log {"lines":40,"path":"app.log"}
  ← obs    ... ERROR connection refused on 127.0.0.1:8080 ...   (0ms)
▸ think  Search the workspace for error indicators.
  → tool   grep {"pattern":"(?i)(error|fail...|refused|...)","path":"."}

✔ result
Root cause: nginx.conf is configured for port 9090, but the goal expects port 8080.
Recommended fix: update nginx.conf to use port 8080 (change 9090 -> 8080) and restart the service.

status: success   steps: 14   elapsed: 3ms
```

Ask it to **fix** instead of diagnose and it rewrites the config itself:

```console
$ praxis run -C examples/broken-web "fix the config so the gateway listens on port 8080"
...
Fix applied: rewrote nginx.conf to the expected port. Restart the service and re-check.
```

And the **web workbench** (`praxis serve`) gives you a goal box, a live
step-by-step trace, a tool-call pipeline that lights up as it runs, a
fast parallel scan, and tools / memory / run-history panels.

---

## Why it exists

The AI-agent ecosystem is a stack of wrappers around a stack of wrappers.
"I built a RAG chatbot with LangChain" says *you glued libraries*. Praxis is the
opposite: implement the agent internals yourself and keep the dependency list
empty. It's meant to show that the agent *runtime* — the part everyone else
imports — is understood end to end.

What's actually built from scratch here:

| Component | What it is | Package |
|---|---|---|
| **Event journal + replay** | Append-only event log; every run is a pure fold over events, so any run replays deterministically | `internal/journal`, `internal/core` |
| **Typed tool protocol** | JSON-schema-style params, validation/coercion, registry, dispatch with retry/backoff — hand-built "function calling" | `internal/tool` |
| **Sandbox** | Escape-proof workspace path jail, command allowlist, timeouts, dry-run | `internal/sandbox` |
| **Reasoner** | `Reasoner` interface + a deterministic heuristic backend **and** an OpenAI-compatible local-model client with its own JSON action protocol | `internal/reason` |
| **ReAct agent loop** | plan → act → observe, step budget, loop guard, self-correction | `internal/agent` |
| **HNSW vector memory** | Feature-hashing text vectorizer + a hand-built HNSW ANN graph index; long-term recall across runs | `internal/memory` |
| **Graph engine** | Typed workflow engine: conditional edges, parallel fan-out/fan-in, bounded loops | `internal/graph` |
| **HTTP server + SSE** | stdlib REST API + Server-Sent-Events live trace, serving an embedded SPA | `internal/server` |
| **Web workbench UI** | Framework-free single-page app | `internal/server/webui` |

## Architecture

```
  praxis run ─┐                         ┌──────────────┐
              ├──▶     Agent      ──────▶│   Reasoner   │  heuristic (no model)
  praxis serve┘   (ReAct control loop)  │  (the brain) │  or OpenAI-compatible
                        │               └──────────────┘  local model over HTTP
        plan/act/observe│
                        ▼
                 ┌────────────┐   dispatch    ┌───────────────────────────┐
                 │   Tools    │ ─────────────▶│ sandbox (jail + allowlist) │
                 │  registry  │               │ read_file grep tail_log    │
                 └────────────┘               │ write_file run_cmd probe   │
                        │                      └───────────────────────────┘
                        │ every step is an event
                        ▼
                 ┌────────────┐   fold    ┌───────────────┐
                 │  Journal   │ ─────────▶│  RunState     │  deterministic replay
                 │ (append)   │           │ + live SSE    │
                 └────────────┘           └───────────────┘
                        ▲
                        │ recall / store
                 ┌────────────┐
                 │  Memory    │  custom HNSW vector index (from scratch)
                 └────────────┘

  graph engine  ──▶  Scan playbook (parallel, model-free triage)  ──▶ praxis scan
```

## Quickstart

```sh
git clone https://github.com/Atishay8192261/praxis
cd praxis
go build -o praxis ./cmd/praxis     # zero third-party dependencies

# 1) Diagnose a broken service (deterministic, no model)
./praxis run -C examples/broken-web "diagnose why the web service is unreachable on port 8080"

# 2) Let it fix the config
./praxis run -C examples/broken-web "fix the config so the gateway listens on port 8080"

# 3) Fast parallel scan (graph playbook)
./praxis scan -C examples/broken-web

# 4) Long-term memory
./praxis memory add "connection refused on a port usually means a wrong listen directive"
./praxis run -C examples/broken-web -memory mem.json "why is the gateway refusing connections"

# 5) Web workbench
./praxis serve -C examples/broken-web        # then open http://localhost:7777
```

### Using a real model

Praxis speaks the OpenAI chat API, so any local server works (Ollama, llama.cpp,
LM Studio, vLLM). Point it at the endpoint and Praxis uses it as the brain:

```sh
export PRAXIS_MODEL_URL=http://127.0.0.1:11434/v1   # e.g. Ollama
export PRAXIS_MODEL_NAME=llama3.1
./praxis run -backend model -C examples/broken-web "the health endpoint is failing, investigate and fix"
```

No env set → Praxis uses the deterministic heuristic backend automatically.

## Benchmarks

Apple M-class laptop, single-threaded, `go test -bench` (see `internal/memory/bench_test.go`):

| Benchmark | Result |
|---|---|
| HNSW search, 10k × 128-dim vectors | **~2,800 queries/sec** (357 µs/query) |
| HNSW recall@10 vs brute force (800 vectors) | **1.00** |
| Text embedding (unigram+bigram hashing) | **~354,000 embeds/sec** (2.8 µs) |
| HNSW build | ~1,800 inserts/sec |
| End-to-end diagnosis (heuristic backend) | **~3 ms** for a full run |

## Zero dependencies — verify it

```sh
$ cat go.mod
module github.com/Atishay8192261/praxis
go 1.23
# no require block

$ go list -m all        # only this module
github.com/Atishay8192261/praxis
```

## How the agent works

1. `run_started` is journaled. Long-term memory (if enabled) is queried for the goal.
2. Each loop iteration folds the journal into `RunState` and asks the `Reasoner`
   for the next `Decision` (a tool call or a final answer), given the goal,
   transcript, tool specs, and recalled memories.
3. A tool call is validated against its schema, dispatched through the sandbox,
   timed, and its `Observation` is journaled — so the reasoner sees the result
   on the next step and can self-correct.
4. On finish, the outcome is stored to memory. Because a run *is* its event log,
   `praxis run -journal run.jsonl` writes a file you can replay exactly.

## Development

```sh
make build      # build ./praxis
make test       # go test ./...
make race       # go test -race ./...
make bench      # benchmarks
make serve      # build + serve the broken-web demo
```

## Roadmap

- [x] Event-sourced core + append-only journal with deterministic replay
- [x] Typed tool protocol, sandbox, and DevOps toolset
- [x] Reasoner interface: model-free heuristic + local-model backends
- [x] End-to-end ReAct agent (diagnose **and** fix)
- [x] Custom HNSW vector memory + graph workflow engine
- [x] HTTP server + SSE live trace + web workbench
- [x] Benchmarks, tests, CI, Docker, docs
- [ ] Multi-agent coordination over the shared blackboard
- [ ] Streaming token output from the model backend

## License

MIT © Atishay Jain
