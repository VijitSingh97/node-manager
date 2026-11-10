import { formatBytes } from "./nodes.js";
import { NODE_LABELS } from "./chains.js";

export const NODE_COLORS = {
  bitcoin: "#f2a900",
  monero: "#ff6600",
  tari: "#8f5cff",
  litecoin: "#4d8fd8",
  dogecoin: "#b88746",
  bitcoincash: "#36a852",
  zcash: "#d69c35",
};

export function diskView(host, sizes) {
  const total = Number(host?.disk_total_bytes);
  if (host?.disk_available !== true || !Number.isFinite(total) || total <= 0) return null;
  const free = Math.min(Math.max(0, Number(host?.disk_free_bytes) || 0), total);
  const used = total - free;
  const pct = (bytes) => (bytes / total) * 100;
  const slices = [];
  let accounted = 0;
  for (const [id, bytes] of sizes) {
    const value = Math.floor(Number(bytes));
    if (!(value > 0)) continue;
    accounted += value;
    slices.push({ id, color: NODE_COLORS[id] || "#9a9a9a", percent: pct(value), title: `${NODE_LABELS[id] || id} — ${formatBytes(value)} (${pct(value).toFixed(2)}%)` });
  }
  const other = Math.max(0, used - accounted);
  slices.push({ id: "other", color: "#9a9a9a", percent: pct(other), title: `Other data — ${formatBytes(other)} (${pct(other).toFixed(2)}%)` });
  slices.push({ id: "free", color: "transparent", percent: pct(free), title: `Free — ${formatBytes(free)} (${pct(free).toFixed(2)}%)` });
  return { line: `Disk ${formatBytes(total)} · ${pct(used).toFixed(1)}% used`, slices };
}

const sizes = new Map();
let host = null;

function render() {
  const panel = document.getElementById("host-stats");
  if (!panel) return;
  const bitcoinBytes = Number(document.getElementById("live")?.dataset.nodeSize);
  if (bitcoinBytes > 0) sizes.set("bitcoin", bitcoinBytes);
  const view = diskView(host, sizes);
  if (!view) return;
  panel.querySelector("[data-host-line]").textContent = view.line;
  const bar = panel.querySelector("[data-host-disk]");
  bar.textContent = "";
  for (const slice of view.slices) {
    const part = document.createElement("div");
    part.style.width = `${Math.max(0, Math.min(100, slice.percent))}%`;
    part.style.background = slice.color;
    part.title = slice.title;
    bar.appendChild(part);
  }
  panel.hidden = false;
}

if (typeof window !== "undefined" && typeof document !== "undefined") {
  window.addEventListener("host-stats-data", (event) => {
    host = event.detail || null;
    render();
  });
  window.addEventListener("node-size", (event) => {
    const { id, bytes } = event.detail || {};
    if (id) sizes.set(id, Number(bytes) || 0);
    render();
  });
}
