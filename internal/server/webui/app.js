"use strict";

const $ = (id) => document.getElementById(id);
const el = (tag, cls, text) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
};

let currentSource = null;
const pnodes = new Map(); // tool call id -> pipeline element

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
}

// ---- tabs ----
document.querySelectorAll(".tabs button").forEach((b) => {
  b.addEventListener("click", () => {
    document.querySelectorAll(".tabs button").forEach((x) => x.classList.remove("active"));
    document.querySelectorAll(".tabpane").forEach((x) => x.classList.remove("active"));
    b.classList.add("active");
    $("tab-" + b.dataset.tab).classList.add("active");
    if (b.dataset.tab === "memory") loadMemory("");
    if (b.dataset.tab === "runs") loadRuns();
  });
});

// ---- tools ----
async function loadTools() {
  try {
    const tools = await api("GET", "/api/tools");
    const box = $("tab-tools");
    box.innerHTML = "";
    tools.forEach((t) => {
      const d = el("div", "tool");
      d.appendChild(el("div", "name", t.name));
      d.appendChild(el("div", "desc", t.description));
      box.appendChild(d);
    });
  } catch (e) {
    $("tab-tools").textContent = "could not load tools: " + e.message;
  }
}

// ---- memory ----
async function loadMemory(q) {
  try {
    const res = await api("GET", "/api/memory" + (q ? "?q=" + encodeURIComponent(q) : ""));
    const box = $("memList");
    box.innerHTML = "";
    if (!res || res.length === 0) {
      box.appendChild(el("div", "empty", "no memories yet"));
      return;
    }
    res.forEach((h) => {
      const rec = h.record || h;
      const d = el("div", "mem");
      if (h.score != null) d.appendChild(el("span", "score", h.score.toFixed(3) + "  "));
      d.appendChild(document.createTextNode(rec.text));
      box.appendChild(d);
    });
  } catch (e) {
    $("memList").textContent = e.message;
  }
}
$("memQuery").addEventListener("input", (e) => loadMemory(e.target.value.trim()));

// ---- runs ----
async function loadRuns() {
  try {
    const runs = await api("GET", "/api/runs");
    const box = $("tab-runs");
    box.innerHTML = "";
    if (!runs || runs.length === 0) {
      box.appendChild(el("div", "empty", "no runs yet"));
      return;
    }
    runs.reverse().forEach((r) => {
      const d = el("div", "tool");
      d.appendChild(el("div", "name", r.status + " · " + r.steps + " steps"));
      d.appendChild(el("div", "desc", r.goal));
      d.style.cursor = "pointer";
      d.addEventListener("click", () => replay(r.id));
      box.appendChild(d);
    });
  } catch (e) {
    $("tab-runs").textContent = e.message;
  }
}

async function replay(id) {
  const st = await api("GET", "/api/runs/" + id);
  resetTrace();
  $("goal").value = st.goal;
  (st.steps || []).forEach(renderStep);
  finish(st.status, (st.steps || []).length);
}

// ---- run lifecycle ----
function resetTrace() {
  if (currentSource) { currentSource.close(); currentSource = null; }
  pnodes.clear();
  $("trace").innerHTML = "";
  $("pipeline").innerHTML = "";
  $("result").innerHTML = "";
  $("statusLine").textContent = "";
}

$("runBtn").addEventListener("click", startRun);
$("scanBtn").addEventListener("click", runScan);

async function startRun() {
  const goal = $("goal").value.trim();
  if (!goal) { $("goal").focus(); return; }
  resetTrace();
  $("runBtn").disabled = true;
  $("statusLine").textContent = "starting…";
  try {
    const res = await api("POST", "/api/run", {
      goal,
      dry_run: $("dryRun").checked,
      max_steps: parseInt($("maxSteps").value, 10) || 12,
      backend: $("backend").value,
    });
    $("statusLine").textContent = "running (" + res.backend + ")…";
    stream(res.run_id);
  } catch (e) {
    $("statusLine").textContent = "error: " + e.message;
    $("runBtn").disabled = false;
  }
}

function stream(id) {
  const src = new EventSource("/api/runs/" + id + "/events");
  currentSource = src;
  src.onmessage = (m) => {
    const ev = JSON.parse(m.data);
    renderEvent(ev);
  };
  src.addEventListener("end", () => {
    src.close();
    currentSource = null;
    $("runBtn").disabled = false;
    loadRuns();
    api("GET", "/api/runs/" + id).then((st) => finish(st.status, (st.steps || []).length)).catch(() => {});
  });
  src.onerror = () => { $("runBtn").disabled = false; };
}

// data may arrive as an event {kind,data} or a materialized step {kind,...}
function renderEvent(ev) {
  const d = ev.data || {};
  switch (ev.kind) {
    case "plan":        return renderStep({ kind: "plan", thought: d.thought });
    case "tool_call":   return renderStep({ kind: "tool_call", tool_call: d });
    case "observation": return renderStep({ kind: "observation", observation: d });
    case "final":       return renderStep({ kind: "final", final: d.result });
    case "error":       return renderStep({ kind: "error", error: d.error });
  }
}

function renderStep(s) {
  const map = { plan: "k-plan", tool_call: "k-tool", observation: "k-obs", final: "k-final", error: "k-error" };
  const label = { plan: "think", tool_call: "tool", observation: "obs", final: "result", error: "error" };

  if (s.kind === "final") {
    const r = el("div", "result", s.final || "(no result)");
    $("result").appendChild(r);
    return;
  }

  const step = el("div", "step " + (map[s.kind] || ""));
  const head = el("div", "head");
  head.appendChild(el("span", "kind", label[s.kind] || s.kind));

  if (s.kind === "tool_call" && s.tool_call) {
    head.appendChild(el("span", null, s.tool_call.name));
    const body = el("div", "body", JSON.stringify(s.tool_call.args || {}, null, 0));
    step.appendChild(head);
    step.appendChild(body);
    addPipelineNode(s.tool_call.id, s.tool_call.name);
  } else if (s.kind === "observation" && s.observation) {
    const o = s.observation;
    if (o.error) step.classList.add("failed");
    const meta = el("span", "meta", (o.elapsed_ms || 0) + " ms");
    head.appendChild(meta);
    const body = el("div", "body", o.error ? "ERROR: " + o.error : (o.output || "(empty)"));
    step.appendChild(head);
    step.appendChild(body);
    markPipelineDone(o.call_id);
  } else if (s.kind === "plan") {
    step.appendChild(head);
    step.appendChild(el("div", "body", s.thought || ""));
  } else if (s.kind === "error") {
    step.appendChild(head);
    step.appendChild(el("div", "body", s.error || ""));
  } else {
    step.appendChild(head);
  }

  $("trace").appendChild(step);
  step.scrollIntoView({ behavior: "smooth", block: "end" });
}

function addPipelineNode(id, name) {
  const n = el("span", "pnode active", name);
  pnodes.set(id, n);
  $("pipeline").appendChild(n);
}
function markPipelineDone(id) {
  const n = pnodes.get(id);
  if (n) { n.classList.remove("active"); n.classList.add("done"); }
}

function finish(status, steps) {
  $("statusLine").textContent = "status: " + status + "  ·  " + steps + " steps";
}

async function runScan() {
  resetTrace();
  $("scanBtn").disabled = true;
  $("statusLine").textContent = "scanning…";
  try {
    const res = await api("POST", "/api/scan", {});
    (res.graph || []).forEach((n) => {
      const e = el("span", "pnode done", n);
      $("pipeline").appendChild(e);
    });
    $("result").appendChild(el("div", "result", res.report));
    $("statusLine").textContent = "scan complete";
  } catch (e) {
    $("statusLine").textContent = "error: " + e.message;
  } finally {
    $("scanBtn").disabled = false;
  }
}

// ---- boot ----
async function boot() {
  loadTools();
  try {
    const info = await api("GET", "/api/info");
    $("backendBadge").textContent = "backend: " + info.backend;
    $("meta").textContent = "workspace: " + info.workspace + (info.memory ? " · memory on" : "");
  } catch (e) {}
}
boot();
