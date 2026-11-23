<h1 align="center">LogFold</h1>

[**Open LogFold**](https://atishay8192261.github.io/logfold/) · [Quick triage](https://atishay8192261.github.io/logfold/triage.html) · [CLI usage](#command-line)

LogFold compares logs from a software incident with a reference window. It groups repeated failures into fingerprints, shows when they spiked and which files contain them, and exports the report as JSON or Markdown. **Run sample** demonstrates a checkout outage with JSON Lines and an earlier plain-text baseline.

The browser workbench is free and runs without an account or API key. It reads plain text, JSON Lines, and gzip locally in a Web Worker. Copy an editable brief into your own ChatGPT session when you want a second pass. Review generated evidence before sharing it.

## How the comparison works

| Stage | Implementation |
|---|---|
| Ingest | A chunked Go reader frames UTF-8 lines. It preserves source line numbers and skips invalid or oversized lines. The same engine runs in a native CLI and as browser WebAssembly. |
| Fingerprint | The engine extracts severity, message, and timestamp from supported formats. It expands common level aliases, masks common credentials, normalizes UUIDs and volatile request/trace fields, and hashes severity plus normalized text with SHA-256. |
| Compare | Counts remain exact for retained fingerprints. Rates use valid lines in each window as their denominator, so matching volume and event frequency are visible even when the windows differ in size. |
| Correlate | Each group records the first and last timestamp, a sample with its file and line, and event counts by source. The UTC histogram starts at one minute and doubles bucket width as needed to stay within 72 buckets, including late or out-of-order events. |
| Bound resources | The browser accepts up to six files and 64 MiB after decompression. The Go engine caps lines at 64 KiB, retains at most 2,048 groups, and caps per-group source details. Overflow stays visible in the totals and timeline. |

LogFold flags explicit warning, error, and fatal events, plus common error phrases when severity is missing. JSON timestamps support RFC 3339 and Unix seconds or milliseconds. Plain-text timestamps need a leading RFC 3339 value or epoch. Untimed events remain in the report and can be compared by fingerprint. Fingerprints describe matching log messages and the rates are per 1,000 valid lines.

## Command line

Build with Go 1.23 or later. The CLI streams files and stdin and writes the same versioned JSON schema as the browser.

```sh
git clone https://github.com/Atishay8192261/logfold && cd logfold
make build
./logfold compare -baseline before.log after.log.gz worker.jsonl > incident-report.json
cat worker.jsonl | ./logfold compare -
```

Repeat `-baseline` to include more reference files. The browser also has [Quick triage](https://atishay8192261.github.io/logfold/triage.html) for pasted log or config snippets.

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
git clone https://github.com/Atishay8192261/logfold && cd logfold
go build -o logfold ./cmd/logfold        # no dependencies to download

# Diagnose a deliberately-broken demo service (no AI model required):
./logfold run  -C examples/broken-web "diagnose why the web service is unreachable on port 8080"

# Preview the fix before applying it:
./logfold run -dry-run -C examples/broken-web "fix the config so the gateway listens on port 8080"

# A fast, parallel, model-free triage of any workspace:
./logfold scan -C examples/broken-web

# Launch the local workbench at http://127.0.0.1:7777
./logfold serve -C examples/broken-web
```

The local workbench can read and change the selected workspace. Run it on loopback as a single-user tool. For Docker, bind the published port to loopback with `-p 127.0.0.1:7777:7777`.

**Connect a local model** (optional). Configure an OpenAI-compatible chat-completions endpoint:

```sh
export PRAXIS_MODEL_URL=http://127.0.0.1:11434/v1   # e.g. Ollama
export PRAXIS_MODEL_NAME=llama3.1
./logfold run -backend model -dry-run -C examples/broken-web "diagnose why the health endpoint is failing"
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

To reproduce the streaming benchmark, run `go test ./internal/logscan -run '^$' -bench BenchmarkStreamLogs -benchmem`. The fixture contains 10,000 mixed plain-text and JSON Lines entries with 10% errors. It exercises framing, normalization, fingerprinting, and report generation in a single pass.

Single-threaded, `go test -bench` (see `internal/memory/bench_test.go`):

| Benchmark | Result |
|---|---|
| HNSW search — 10k × 128-dim vectors | **~2,800 queries/sec** (357 µs) |
| HNSW recall@10 vs. brute force | **1.00** |
| Text embedding | **~354,000 embeds/sec** (2.8 µs) |
| End-to-end diagnosis (heuristic backend) | **~3 ms** per run |

## Development

```sh
make build      # build ./logfold
make test       # go test ./...
make race       # go test -race ./...
make bench      # benchmarks
make serve      # build + serve the local demo workspace
make site       # build both browser tools into dist/site
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
