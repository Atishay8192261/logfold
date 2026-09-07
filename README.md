# Praxis

**A from-scratch AI-agent orchestration runtime — with zero third-party dependencies.**

Praxis is the layer that agent frameworks like LangGraph, CrewAI, and AutoGen
sell — a graph executor, a planning loop, a function-calling tool protocol,
vector-backed memory, and deterministic replay — except every line of it is
built on the Go standard library. No orchestration framework, no vector DB, no
HTTP framework. You get to see exactly how an autonomous agent actually works,
because there is nothing imported to hide behind.

On top of the runtime, Praxis ships a real product: a **DevOps automation
agent** that you point at a broken service and a goal ("diagnose why the web
service is down"). It plans, inspects files/configs/processes/ports with
sandboxed tools, and proposes or applies a fix — while a live web workbench
streams every `plan → tool-call → observation` step.

> **The model is not a dependency, it is a service.** Praxis talks to a
> reasoning model over HTTP through a `Reasoner` interface you can swap. It also
> ships a deterministic, model-free backend, so the entire system builds, tests,
> and demos with no model at all.

---

## Why it exists

The AI-agent ecosystem is a stack of wrappers around a stack of wrappers.
Praxis is a study in the opposite direction: implement the agent internals
yourself and keep the dependency list empty.

- **Graph executor** — an agent is a directed graph of typed nodes with
  conditional edges, bounded loops, and parallel fan-out/fan-in.
- **ReAct control loop** — plan → act → observe → repeat, with a step budget,
  stop conditions, and self-correction on tool errors.
- **Typed tool protocol** — tools carry JSON schemas; arguments are validated,
  dispatched, retried, and recorded.
- **Memory** — a working scratchpad plus a from-scratch HNSW vector index for
  long-term semantic recall, with context-window management.
- **Event-sourced replay** — every run is an append-only event log; any run
  can be replayed deterministically for debugging.

## Status

Under active construction, milestone by milestone:

- [x] **P1** — core event model + append-only journal with deterministic replay
- [ ] **P2** — tool protocol, sandbox, and DevOps tools
- [ ] **P3** — reasoner interface + model-free and local-model backends
- [ ] **P4** — end-to-end ReAct agent (headless CLI)
- [ ] **P5** — graph executor + HNSW vector memory
- [ ] **P6** — HTTP server + live SSE traces
- [ ] **P7** — web workbench UI
- [ ] **P8** — demo scenario, benchmarks, CI, Docker, docs

## Build

```sh
go build ./...      # zero third-party dependencies
go test ./...
```

## License

MIT © Atishay Jain
