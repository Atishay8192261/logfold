"use strict";

const examples = {
  gateway: {
    symptom: "Gateway health checks on port 8080 fail after the config change.",
    logs: "14:32:06 INFO  gateway booted\n14:32:07 INFO  applied nginx.conf\n14:32:12 ERROR GET http://127.0.0.1:8080/health: connection refused\n14:32:17 ERROR GET http://127.0.0.1:8080/health: connection refused",
    config: 'server {\n  listen 9090;\n  server_name _;\n  location /health {\n    return 200 "ok";\n  }\n}',
  },
  database: {
    symptom: "Checkout requests fail after the database host changed.",
    logs: "09:42:15 INFO  starting checkout-api\n09:42:17 ERROR failed to connect to postgres: no such host (postgres-primary)\n09:42:22 WARN  startup retry deadline exceeded\n09:42:24 ERROR GET /checkout status=503",
    config: "DB_HOST=postgres-primary\nDB_PORT=5432\nDB_PASSWORD=sample-fixture-only\nDB_TIMEOUT_SECONDS=5",
  },
  build: {
    symptom: "The new frontend release exits on startup.",
    logs: "10:06:03 INFO  starting built frontend\n10:06:04 ERROR Cannot find module 'dotenv'\n10:06:04 ERROR code: MODULE_NOT_FOUND\n10:06:05 INFO  process exited with status 1",
    config: "FROM node:24-alpine\nWORKDIR /app\nCOPY package*.json ./\nRUN npm ci --omit=dev\nCOPY . .\nCMD [\"node\", \"server.js\"]",
  },
};

const byId = (id) => document.getElementById(id);
const fields = { symptom: byId("symptom"), logs: byId("logs"), config: byId("config") };
const result = byId("report");
const empty = byId("empty-report");
const meta = byId("report-meta");
const indicator = byId("report-indicator");
const analyze = byId("analyze");
const brief = byId("brief");
const message = byId("input-error");
const copyStatus = byId("copy-status");
const sampleButtons = document.querySelectorAll(".sample");
const limit = 192 * 1024;
const fileReads = new Map();

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text) node.textContent = text;
  if (className) node.className = className;
  return node;
}

function showError(text) {
  message.textContent = text;
  message.hidden = !text;
}

function clearReport() {
  result.hidden = true;
  empty.hidden = false;
  indicator.classList.remove("ready");
  meta.textContent = "Findings and checks to run next.";
  byId("findings").replaceChildren();
  brief.value = "";
  copyStatus.textContent = "";
  byId("redactions").textContent = "";
}

function findingCard(finding) {
  const card = element("article", "", "finding");
  card.append(element("h3", finding.title), element("p", finding.meaning));
  const check = element("p");
  check.append(element("strong", "Next check. "), finding.check);
  card.append(check);

  const details = element("details");
  details.open = true;
  details.append(element("summary", `Evidence · ${finding.matches} matching ${finding.matches === 1 ? "line" : "lines"}`));
  const list = element("ul", "", "evidence");
  for (const item of finding.evidence) {
    const row = element("li");
    row.append(element("span", `${item.source} line ${item.line}`), element("code", item.text));
    list.append(row);
  }
  details.append(list);
  card.append(details);
  return card;
}

function render(report) {
  const findings = byId("findings");
  if (report.findings.length) {
    findings.replaceChildren(...report.findings.map(findingCard));
  } else {
    const card = element("article", "", "no-findings");
    card.append(
      element("h3", "No rule matched this input"),
      element("p", "Try including the first failure, the lines around it, and the settings for the affected service. The brief below includes excerpts for further triage."),
    );
    findings.replaceChildren(card);
  }
  const count = report.findings.length;
  meta.textContent = `${count} ${count === 1 ? "finding" : "findings"} · ${report.lines.toLocaleString()} ${report.lines === 1 ? "line" : "lines"} inspected`;
  byId("redactions").textContent = report.redactions
    ? `Masked ${report.redactions} possible ${report.redactions === 1 ? "secret" : "secrets"}. Check for anything the filter missed.`
    : "Check the brief for credentials and private details before sharing.";
  brief.value = report.brief;
  copyStatus.textContent = "";
  empty.hidden = true;
  result.hidden = false;
  indicator.classList.add("ready");
}

function inspect(scroll) {
  showError("");
  const input = Object.fromEntries(Object.entries(fields).map(([key, field]) => [key, field.value]));
  try {
    const response = JSON.parse(globalThis.logfoldAnalyze(JSON.stringify(input)));
    if (response.error) {
      clearReport();
      showError(response.error);
      return;
    }
    render(response.report);
    if (scroll && window.matchMedia("(max-width: 760px)").matches) {
      byId("report-heading").focus({ preventScroll: true });
      byId("report-heading").scrollIntoView({ block: "start" });
    }
  } catch {
    clearReport();
    showError("The analysis could not finish. Reload LogFold and try again.");
  }
}

function onInput() {
  clearReport();
  showError("");
}

byId("incident").addEventListener("submit", (event) => {
  event.preventDefault();
  if (!analyze.disabled) inspect(true);
});

for (const field of Object.values(fields)) {
  field.addEventListener("input", () => {
    fileReads.delete(field);
    onInput();
  });
}

byId("clear").addEventListener("click", () => {
  fileReads.clear();
  for (const field of Object.values(fields)) field.value = "";
  onInput();
  fields.symptom.focus();
});

for (const button of sampleButtons) {
  button.disabled = true;
  button.addEventListener("click", () => {
    fileReads.clear();
    const sample = examples[button.dataset.example];
    for (const [name, field] of Object.entries(fields)) field.value = sample[name];
    inspect(false);
    byId("report-heading").focus();
  });
}

for (const button of document.querySelectorAll(".file-button")) {
  const input = byId(button.dataset.file);
  const field = input.id === "log-file" ? fields.logs : fields.config;
  button.addEventListener("click", () => input.click());
  input.addEventListener("change", async () => {
    const [file] = input.files;
    input.value = "";
    if (!file) return;
    const request = Symbol();
    fileReads.set(field, request);
    showError("");
    try {
      if (file.size > limit) throw new Error("Choose a text file up to 192 KiB.");
      const text = new TextDecoder("utf-8", { fatal: true }).decode(await file.arrayBuffer());
      if (fileReads.get(field) !== request) return;
      if (text.includes("\0")) throw new Error("Choose a plain-text file.");
      field.value = text;
      onInput();
    } catch (error) {
      if (fileReads.get(field) !== request) return;
      showError(error instanceof TypeError ? "Choose a UTF-8 text file." : error.message || "Could not open this file.");
    } finally {
      if (fileReads.get(field) === request) fileReads.delete(field);
    }
  });
}

brief.addEventListener("input", () => { copyStatus.textContent = ""; });

byId("copy-brief").addEventListener("click", async () => {
  const text = brief.value;
  if (!text.trim()) {
    copyStatus.textContent = "Add some text to the brief before copying.";
    brief.focus();
    return;
  }
  try {
    await navigator.clipboard.writeText(text);
    if (brief.value === text) copyStatus.textContent = "Copied. Paste the brief into your ChatGPT conversation.";
  } catch {
    if (brief.value !== text) return;
    brief.focus();
    brief.select();
    copyStatus.textContent = "Clipboard access is unavailable. Use your device’s Copy command on the selected text.";
  }
});

function engineError() {
  analyze.disabled = true;
  for (const button of sampleButtons) button.disabled = true;
  showError("LogFold could not start the browser engine. Reload the page or try a current browser.");
}

async function start() {
  const runtime = new Go();
  const response = await fetch("./triage.wasm");
  if (!response.ok) throw new Error("Engine download failed");
  const { instance } = await WebAssembly.instantiate(await response.arrayBuffer(), runtime.importObject);
  runtime.run(instance).then(engineError, engineError);
  if (typeof globalThis.logfoldAnalyze !== "function") throw new Error("Engine initialization failed");
  analyze.disabled = false;
  for (const button of sampleButtons) button.disabled = false;
}

start().catch(engineError);
