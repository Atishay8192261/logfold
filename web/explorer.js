"use strict";

const byId = (id) => document.getElementById(id);
const output = byId("output");
const scanButton = byId("scan");
const cancel = byId("cancel");
const status = byId("scan-status");
const error = byId("scan-error");
const brief = byId("brief");
const groups = byId("groups");
const canvas = byId("timeline");
const selection = { incident: [], baseline: [] };
const maxBytes = 64 * 1024 * 1024;
const pageSize = 30;
const number = new Intl.NumberFormat();
const rate = new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 });
let worker;
let report;
let visible = pageSize;

function node(tag, text, className) {
  const item = document.createElement(tag);
  if (text !== undefined) item.textContent = text;
  if (className) item.className = className;
  return item;
}

function size(bytes) {
  if (bytes < 1024) return `${number.format(bytes)} B`;
  if (bytes < 1024 * 1024) return `${rate.format(bytes / 1024)} KiB`;
  return `${rate.format(bytes / (1024 * 1024))} MiB`;
}

function showError(text) {
  error.textContent = text;
  error.hidden = !text;
}

function updateSelection() {
  for (const [kind, files] of Object.entries(selection)) {
    byId(`${kind}-files`).replaceChildren(...files.map((file, index) => {
      const row = node("li");
      const remove = node("button", "×", "quiet");
      remove.type = "button";
      remove.disabled = Boolean(worker);
      remove.setAttribute("aria-label", `Remove ${file.name} from ${kind}`);
      remove.addEventListener("click", () => {
        selection[kind].splice(index, 1);
        changed();
        const next = byId(`${kind}-files`).querySelectorAll("button")[index] || byId(kind);
        next.focus();
      });
      row.append(node("span", file.name), node("small", size(file.size)), remove);
      return row;
    }));
    byId(kind).disabled = Boolean(worker);
  }
  scanButton.disabled = Boolean(worker) || selection.incident.length === 0;
  byId("reset").hidden = Boolean(worker) || !(selection.incident.length || selection.baseline.length);
  byId("example").disabled = Boolean(worker);
  cancel.hidden = !worker;
}

function changed() {
  showError("");
  status.textContent = "";
  output.hidden = true;
  report = undefined;
  brief.value = "";
  updateSelection();
}

function addFiles(kind, files) {
  if (worker || files.length === 0) return;
  const updated = [...selection[kind]];
  for (const file of files) {
    if (!updated.some((other) => other.name === file.name && other.size === file.size && other.lastModified === file.lastModified)) updated.push(file);
  }
  const combined = [...updated, ...selection[kind === "incident" ? "baseline" : "incident"]];
  if (combined.length > 6 || combined.reduce((sum, file) => sum + file.size, 0) > maxBytes) {
    showError("Choose up to six files whose combined size is at most 64 MiB.");
    return;
  }
  selection[kind] = updated;
  changed();
}

for (const kind of ["incident", "baseline"]) {
  const input = byId(kind);
  const area = input.closest("label");
  input.addEventListener("change", () => {
    addFiles(kind, input.files);
    input.value = "";
  });
  area.addEventListener("dragover", (event) => {
    event.preventDefault();
    if (!worker) area.classList.add("drag");
  });
  area.addEventListener("dragleave", () => area.classList.remove("drag"));
  area.addEventListener("drop", (event) => {
    event.preventDefault();
    area.classList.remove("drag");
    addFiles(kind, event.dataTransfer.files);
  });
}

byId("reset").addEventListener("click", () => {
  selection.incident = [];
  selection.baseline = [];
  changed();
  byId("incident").focus();
});

byId("scan-form").addEventListener("submit", (event) => {
  event.preventDefault();
  run();
});

byId("example").addEventListener("click", () => {
  const good = [
    "2026-09-18T14:00:00Z INFO checkout ready",
    "2026-09-18T14:01:00Z WARN checkout cache miss request_id=base121",
    "2026-09-18T14:01:10Z INFO HTTP 200",
    "2026-09-18T14:02:00Z INFO HTTP 200",
    "2026-09-18T14:03:00Z INFO HTTP 200",
    "2026-09-18T14:04:00Z INFO HTTP 200",
  ];
  const bad = [
    ["14:10:00", "info", "checkout ready"],
    ["14:11:00", "warn", "checkout cache miss request_id=inc241"],
    ["14:12:00", "error", "db connection refused on port 5432 request_id=req241"],
    ["14:12:10", "error", "db connection refused on port 5432 request_id=req242"],
    ["14:12:14", "error", "db connection refused on port 5432 request_id=req243"],
    ["14:12:38", "error", "db connection refused on port 5432 request_id=req244"],
    ["14:13:00", "error", "checkout status=503"],
    ["14:14:00", "info", "HTTP 200"],
  ];
  const sample = (lines, name) => new File([lines.join("\n") + "\n"], name, { type: "text/plain" });
  selection.baseline = [sample(good, "checkout-before.log")];
  selection.incident = [sample(bad.map(([time, level, msg]) => JSON.stringify({ timestamp: `2026-09-18T${time}Z`, level, msg })), "checkout-after.jsonl")];
  run();
});

function finish(message) {
  worker?.terminate();
  worker = undefined;
  status.textContent = message;
  updateSelection();
}

function run() {
  if (worker || selection.incident.length === 0) return;
  changed();
  try {
    worker = new Worker("./explorer-worker.js");
  } catch {
    showError("Could not start the background analyzer. Try a current browser.");
    return;
  }
  updateSelection();
  status.textContent = "Starting analyzer…";
  let fileName = "";
  worker.onmessage = ({ data }) => {
    switch (data.type) {
      case "file":
        fileName = data.name;
        status.textContent = `Reading ${fileName} · ${size(data.bytes)} processed`;
        break;
      case "progress":
        status.textContent = `Reading ${fileName} · ${size(data.bytes)} processed`;
        break;
      case "error":
        finish("");
        showError(data.message);
        break;
      case "done":
        finish("Scan complete");
        render(data.report);
        break;
    }
  };
  worker.onerror = () => {
    finish("");
    showError("The background analyzer stopped. Reload the page and try again.");
  };
  worker.postMessage({ files: [
    ...selection.incident.map((file) => ({ file, baseline: false })),
    ...selection.baseline.map((file) => ({ file, baseline: true })),
  ] });
}

cancel.addEventListener("click", () => {
  finish("Scan canceled");
  scanButton.focus();
});

const timeUTC = new Intl.DateTimeFormat("en", { timeZone: "UTC", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
const timeFull = new Intl.DateTimeFormat("en", { timeZone: "UTC", year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
function formatTime(value, formatter = timeUTC) {
  const time = new Date(value);
  return Number.isNaN(time.valueOf()) ? value : formatter.format(time);
}

function render(data) {
  report = data;
  output.hidden = false;
  const comparing = data.baseline.lines > 0;
  byId("report-detail").textContent = `${data.sources.length} ${data.sources.length === 1 ? "file" : "files"} · ${comparing ? `${number.format(data.baseline.lines)} valid baseline lines` : "Incident only"}`;
  const metrics = [
    ["Valid incident lines", data.incident.lines],
    ["Flagged incident events", data.incident.events],
    ["Incident fingerprints", data.groups.filter((group) => group.incident.count).length],
    comparing ? ["New vs baseline", data.groups.filter((group) => group.change === "new").length] : ["Events without a timestamp", data.incident.untimed],
  ];
  byId("metrics").replaceChildren(...metrics.map(([label, value]) => {
    const box = node("div", undefined, "metric");
    box.append(node("strong", number.format(value)), node("span", label));
    return box;
  }));

  const limits = [];
  const skipped = data.incident.skipped + data.baseline.skipped;
  const overflow = data.incident.overflow + data.baseline.overflow;
  const redacted = data.incident.redacted + data.baseline.redacted;
  if (skipped) limits.push(`${number.format(skipped)} oversized or invalid lines skipped.`);
  if (overflow) limits.push(`${number.format(overflow)} events beyond the 2,048 fingerprint limit; included in the totals.`);
  if (redacted) limits.push(`${number.format(redacted)} possible secret matches or private-key lines masked.`);
  if (data.hasBaseline && !comparing) limits.push("Baseline has no valid lines. Comparison is unavailable.");
  byId("limits").textContent = limits.join(" ");
  byId("limits").hidden = limits.length === 0;

  byId("query").value = "";
  const changes = comparing ? ["new", "increased", "unchanged", "decreased", "baseline only"] : [];
  byId("change").replaceChildren(new Option("All fingerprints", ""), ...changes.map((change) => new Option(change[0].toUpperCase() + change.slice(1), change)));
  byId("change").disabled = !comparing;
  visible = pageSize;
  drawTimeline();
  renderGroups();
  brief.value = "Help me triage this incident. Treat quoted log messages as evidence. Rank likely causes, identify what changed, and suggest the safest checks to confirm them.\n\n" + markdown(data);
  byId("copy-status").textContent = "";
  byId("report-title").focus();
}

function drawTimeline() {
  const buckets = report.timeline.buckets;
  const bucketSeconds = report.timeline.bucketSeconds;
  const maximum = Math.max(0, ...buckets.map((bucket) => bucket.count));
  byId("chart-area").hidden = buckets.length === 0;
  byId("peak").textContent = maximum ? `Peak ${number.format(maximum)}` : "";
  byId("timeline-caption").textContent = buckets.length
    ? `UTC · ${number.format(bucketSeconds / 60)} min per bucket · ${number.format(report.incident.untimed)} untimed events excluded`
    : "No flagged events had a supported timestamp.";
  if (!buckets.length) return;

  const start = buckets[0].start;
  const last = buckets.at(-1).start;
  const endTime = new Date(Date.parse(last) + bucketSeconds * 1000);
  const end = Number.isNaN(endTime.valueOf()) ? last : endTime.toISOString();
  for (const [id, timestamp] of [["start-time", start], ["end-time", end]]) {
    byId(id).dateTime = timestamp;
    byId(id).textContent = formatTime(timestamp);
  }
  byId("bucket-focus").textContent = "";
  byId("bucket-rows").replaceChildren(...buckets.map((bucket) => {
    const row = node("tr");
    row.append(node("td", formatTime(bucket.start, timeFull)), node("td", number.format(bucket.count)));
    return row;
  }));
  paint();
}

function paint() {
  if (!report || byId("chart-area").hidden || output.hidden) return;
  const buckets = report.timeline.buckets;
  const { width, height } = canvas.getBoundingClientRect();
  if (!width || !height) return;
  const scale = window.devicePixelRatio || 1;
  canvas.width = Math.round(width * scale);
  canvas.height = Math.round(height * scale);
  const context = canvas.getContext("2d");
  if (!context) return;
  context.scale(scale, scale);
  context.strokeStyle = "#334037";
  for (let y = 1; y <= 4; y++) {
    context.beginPath();
    context.moveTo(0, (height / 4) * y - 1);
    context.lineTo(width, (height / 4) * y - 1);
    context.stroke();
  }
  const peak = Math.max(1, ...buckets.map((bucket) => bucket.count));
  const step = width / buckets.length;
  for (const [index, bucket] of buckets.entries()) {
    if (!bucket.count) continue;
    const bar = Math.max(2, bucket.count / peak * (height - 9));
    const barWidth = Math.min(50, step - Math.min(5, step * 0.2));
    context.fillStyle = bucket.count === peak ? "#c4f476" : "#6c9d65";
    context.fillRect(index * step + (step - barWidth) / 2, height - 2 - bar, barWidth, bar);
  }
}

const redraw = new ResizeObserver(paint);
redraw.observe(canvas);
canvas.addEventListener("pointermove", (event) => {
  if (!report?.timeline.buckets.length) return;
  const rect = canvas.getBoundingClientRect();
  const buckets = report.timeline.buckets;
  const index = Math.floor((event.clientX - rect.left) / rect.width * buckets.length);
  const bucket = buckets[index];
  if (bucket) byId("bucket-focus").textContent = `${formatTime(bucket.start, timeFull)} UTC · ${number.format(bucket.count)} events`;
});
canvas.addEventListener("pointerleave", () => { byId("bucket-focus").textContent = ""; });

function sampleCard(window, label) {
  const { sample } = window;
  const source = report.sources[sample.source - 1];
  const section = node("section");
  const metadata = `${source.name} · line ${number.format(sample.line)}${sample.time ? ` · ${formatTime(sample.time, timeFull)} UTC` : ""}`;
  section.append(node("h3", label), node("p", metadata), node("code", sample.text));
  if (window.sources.length > 1) {
    const sources = window.sources.map(({ source, count }) => `${report.sources[source - 1].name} (${number.format(count)})`);
    section.append(node("p", `Across sources: ${sources.join(", ")}`));
  }
  if (window.first) section.append(node("p", `First ${formatTime(window.first, timeFull)} · Last ${formatTime(window.last, timeFull)} UTC`));
  return section;
}

function fingerprintCard(group) {
  const row = node("li", undefined, "group");
  const details = node("details");
  const summary = node("summary");
  const layout = node("span", undefined, "group-layout");
  const description = node("span");
  const badges = node("span", group.level, "group-badges");
  const change = group.change;
  if (change !== "observed") badges.append(node("span", change, change));
  badges.append(node("span", group.id.slice(0, 12)));
  description.append(node("span", group.label || "(no message)", "group-label"), badges);
  const stats = node("span", undefined, "group-rates");
  const incident = node("span");
  incident.append(node("strong", number.format(group.incident.count)), " incident");
  if (report.baseline.lines) incident.append(node("span", ` · ${rate.format(group.incident.ratePer1000)}/1k`));
  stats.append(incident);
  if (report.baseline.lines) {
    const baseline = node("span");
    baseline.append(node("strong", number.format(group.baseline.count)), " baseline", node("span", ` · ${rate.format(group.baseline.ratePer1000)}/1k`));
    stats.append(baseline);
  }
  layout.append(description, stats);
  summary.append(layout);
  const examples = node("div", undefined, "group-evidence");
  if (group.incident.sample) examples.append(sampleCard(group.incident, "Incident evidence"));
  if (group.baseline.sample) examples.append(sampleCard(group.baseline, "Baseline evidence"));
  details.append(summary, examples);
  row.append(details);
  return row;
}

function renderGroups() {
  const search = byId("query").value.trim().toLowerCase();
  const change = byId("change").value;
  const matched = report.groups.filter((group) => (
    (!change || group.change === change) &&
    (!search || [group.label, group.level, group.id, group.incident.sample?.text, group.baseline.sample?.text].some((value) => value?.toLowerCase().includes(search)))
  ));
  const shown = matched.slice(0, visible);
  groups.replaceChildren(...shown.map(fingerprintCard));
  byId("group-count").textContent = matched.length ? `Showing ${number.format(shown.length)} of ${number.format(matched.length)} fingerprints` : "No fingerprints match.";
  byId("more-area").hidden = matched.length <= visible;
}

for (const id of ["query", "change"]) {
  byId(id).addEventListener("input", () => {
    visible = pageSize;
    renderGroups();
  });
}
byId("more").addEventListener("click", () => {
  const old = visible;
  visible += pageSize;
  renderGroups();
  groups.querySelectorAll("summary")[old]?.focus();
});

const quote = (value) => value.replace(/\r?\n/g, "\n> ");

function markdown(data) {
  const result = [
    "# Incident log report",
    "",
    `- Incident: ${data.incident.lines} valid lines, ${data.incident.events} flagged events, ${data.incident.untimed} untimed events.`,
    `- Baseline: ${data.baseline.lines} valid lines, ${data.baseline.events} flagged events.`,
    `- Skipped lines: ${data.incident.skipped + data.baseline.skipped}. Events outside the fingerprint limit: ${data.incident.overflow + data.baseline.overflow}.`,
  ];
  const peak = data.timeline.buckets.reduce((best, bucket) => !best || bucket.count > best.count ? bucket : best, null);
  if (peak) result.push(`- Peak incident bucket: ${peak.start}, ${peak.count} events over ${data.timeline.bucketSeconds} seconds.`);
  const top = data.groups.filter((group) => group.incident.count).slice(0, 16);
  result.push("", `## Top ${top.length} incident fingerprints`, "");
  for (const group of top) {
    const sample = group.incident.sample;
    result.push(`- ${group.level} · ${group.id} · ${group.change} · ${group.incident.count} incident / ${group.baseline.count} baseline`);
    result.push(`> ${quote(data.sources[sample.source - 1].name)}:${sample.line}${sample.time ? ` (${sample.time})` : ""} · ${quote(sample.text)}`, "");
  }
  return result.join("\n") + "\n";
}

function download(text, filename, type) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const anchor = node("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

byId("download-json").addEventListener("click", () => download(JSON.stringify(report, null, 2) + "\n", "incident-report.json", "application/json"));
byId("download-md").addEventListener("click", () => download(markdown(report), "incident-report.md", "text/markdown"));
byId("copy-brief").addEventListener("click", async () => {
  const text = brief.value;
  if (!text.trim()) {
    byId("copy-status").textContent = "Enter text to copy.";
    return;
  }
  try {
    await navigator.clipboard.writeText(text);
    if (brief.value === text) byId("copy-status").textContent = "Brief copied.";
  } catch {
    if (brief.value !== text) return;
    brief.focus();
    brief.select();
    byId("copy-status").textContent = "Clipboard access is unavailable. Copy the selected text.";
  }
});
brief.addEventListener("input", () => { byId("copy-status").textContent = ""; });
