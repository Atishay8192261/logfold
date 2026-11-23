"use strict";

importScripts("./wasm_exec.js");

const chunkSize = 64 * 1024;
const maxExpandedBytes = 64 * 1024 * 1024;

self.onmessage = async ({ data }) => {
  try {
    const go = new Go();
    const response = await fetch("./explorer.wasm");
    if (!response.ok) throw new Error("Could not download the analysis engine.");
    const { instance } = await WebAssembly.instantiateStreaming(response, go.importObject);
    go.run(instance).catch(() => postMessage({ type: "error", message: "The analysis engine stopped." }));
    let expanded = 0;
    for (const { file, baseline } of data.files) {
      postMessage({ type: "file", bytes: expanded, name: file.name });
      check(logfoldStartLog(file.name, baseline));
      let stream = file.stream();
      if (file.name.toLowerCase().endsWith(".gz")) {
        if (typeof DecompressionStream !== "function") throw new Error("This browser cannot open gzip. Unzip the file and try again.");
        stream = stream.pipeThrough(new DecompressionStream("gzip"));
      }
      const reader = stream.getReader();
      let lastProgress = performance.now();
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          expanded += value.byteLength;
          if (expanded > maxExpandedBytes) throw new Error("The expanded logs exceed 64 MiB. Use a smaller time window.");
          for (let i = 0; i < value.byteLength; i += chunkSize) {
            check(logfoldLogChunk(value.subarray(i, i + chunkSize)));
          }
          if (performance.now() - lastProgress >= 125) {
            postMessage({ type: "progress", bytes: expanded });
            lastProgress = performance.now();
          }
        }
      } catch (error) {
        await reader.cancel().catch(() => {});
        throw error;
      } finally {
        reader.releaseLock();
      }
      logfoldEndLog();
    }
    postMessage({ type: "done", report: JSON.parse(logfoldLogReport()) });
  } catch (error) {
    postMessage({ type: "error", message: error.message || "Could not read the log files." });
  }
};

function check(message) {
  if (message) throw new Error(message);
}
