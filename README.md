<h1 align="center">Praxis</h1>

[**Open Praxis**](https://atishay8192261.github.io/praxis/) · [Local Go agent](#local-quickstart)

Praxis turns logs and configuration snippets into an incident report with matching lines, hypotheses, and suggested checks. Try the gateway sample, or paste evidence from a failing deployment. You can review and copy the incident brief into your own ChatGPT session or another assistant.

The public workbench runs a rule-based Go analyzer in your browser using WebAssembly. You can use it without an account or API key. It masks common credential patterns in results and excerpts. Review the brief before sharing it, since pattern matching can miss secrets and other private details.

The same repository contains a local Go agent runtime for workspace scans, live traces, and assisted fixes. Run it on a workspace you control.

## Local runtime

- 🧠 **A complete agent runtime, from scratch** — ReAct planning loop, a typed function-calling tool protocol, and a graph workflow engine with parallel execution. No agent framework used.
- 🔒 **Workspace-scoped tools** — file operations use a symlink-aware workspace boundary. The CLI supports command allowlists, timeouts, and dry-run mode.
- 🗂️ **Custom vector memory** — a hand-written **HNSW** approximate-nearest-neighbor index gives the agent long-term recall across runs. *recall@10 = 1.0, ~2,800 queries/sec.*
- ⏪ **Deterministic replay** — every run is an append-only event log, so any run can be replayed exactly for debugging.
- 🔌 **Model options** — use the built-in heuristic or configure an OpenAI-compatible endpoint such as Ollama.
- 🧪 **Build and test** — GitHub Actions CI, race-detector checks, and a distroless Docker image.
- 📦 **Zero third-party dependencies** — `go.mod` has no `require` block. Everything is the standard library.

## Local quickstart

```sh
git clone https://github.com/Atishay8192261/praxis && cd praxis
go build -o praxis ./cmd/praxis        # no dependencies to download

# Diagnose a deliberately-broken demo service (no AI model required):
./praxis run  -C examples/broken-web "diagnose why the web service is unreachable on port 8080"

# Preview the fix before applying it:
./praxis run -dry-run -C examples/broken-web "fix the config so the gateway listens on port 8080"

# A fast, parallel, model-free triage of any workspace:
./praxis scan -C examples/broken-web

# Launch the local workbench at http://127.0.0.1:7777
./praxis serve -C examples/broken-web
```

The local workbench can read and change the selected workspace. Run it on loopback as a single-user tool. For Docker, bind the published port to loopback with `-p 127.0.0.1:7777:7777`.

**Connect a local model** (optional). Configure an OpenAI-compatible chat-completions endpoint:

```sh
export PRAXIS_MODEL_URL=http://127.0.0.1:11434/v1   # e.g. Ollama
export PRAXIS_MODEL_NAME=llama3.1
./praxis run -backend model -dry-run -C examples/broken-web "diagnose why the health endpoint is failing"
```

To use OpenAI’s API from the local agent, set `PRAXIS_MODEL_URL=https://api.openai.com/v1`, set `PRAXIS_MODEL_NAME` to a Chat Completions model available in your API project, and provide your API key through `PRAXIS_MODEL_KEY`. The API has separate billing from ChatGPT. The public workbench’s copy-and-paste handoff uses your ChatGPT session without API credentials.

## How it's built

<p align="center">
  <img src="docs/architecture.svg" alt="Architecture: entrypoints feed the agent runtime, which drives the reasoner, sandboxed tools, and vector memory; every step is journaled for deterministic replay and live streaming" width="100%">
</p>

An agent run is nothing but an ordered list of events. The live view and any later replay are both a pure fold over that log — which is what makes runs reproducible. Each of these pieces is implemented directly on the standard library:

| Component | What it does |
|---|---|
| **Event journal + replay** | Append-only log; every run replays deterministically, even after a crash |
| **Tool protocol** | JSON-schema params, validation, dispatch with retry — hand-built "function calling" |
| **Sandbox** | Symlink-aware path jail + command allowlist + timeouts + dry-run |
| **Reasoner** | Swappable brain: a model-free heuristic backend, or an OpenAI-compatible model client with its own JSON action protocol |
| **ReAct agent loop** | plan → act → observe, with a step budget, loop guard, and self-correction |
| **HNSW vector memory** | From-scratch ANN graph index + text vectorizer for long-term recall |
| **Graph engine** | Typed workflow engine: conditional branches, parallel fan-out/fan-in, bounded loops |
| **HTTP server + SSE** | Standard-library REST API + live event streaming, serving an embedded single-page UI |

## Benchmarks

Single-threaded, `go test -bench` (see `internal/memory/bench_test.go`):

| Benchmark | Result |
|---|---|
| HNSW search — 10k × 128-dim vectors | **~2,800 queries/sec** (357 µs) |
| HNSW recall@10 vs. brute force | **1.00** |
| Text embedding | **~354,000 embeds/sec** (2.8 µs) |
| End-to-end diagnosis (heuristic backend) | **~3 ms** per run |

## Development

```sh
make build      # build ./praxis
make test       # go test ./...
make race       # go test -race ./...
make bench      # benchmarks
make serve      # build + serve the local demo workspace
make site       # build the public workbench into dist/site
```

To preview the public workbench locally, run `python3 -m http.server --bind 127.0.0.1 --directory dist/site 8000` and open `http://127.0.0.1:8000`. GitHub Actions publishes the static build from `main`.

To list Go dependencies: `cat go.mod` shows no `require` block, and `go list -m all` lists only this module.

## Roadmap

- [x] Event-sourced runtime with deterministic replay
- [x] Sandboxed DevOps agent that diagnoses **and** fixes
- [x] Custom HNSW vector memory + parallel graph engine
- [x] Web workbench with live streaming
- [x] Benchmarks, CI, Docker, security-hardening pass
- [ ] Multi-agent coordination over a shared blackboard
- [ ] Streaming token output from the model backend

## License

MIT © Atishay Jain
