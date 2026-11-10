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

export function pieView(stats) {
  const cpus = Number(stats?.cpus);
  const memTotal = Number(stats?.mem_total_bytes);
  if (!(cpus > 0) || !(memTotal > 0)) return null;
  const cpu = [];
  const mem = [];
  let cpuSum = 0;
  let memSum = 0;
  const entries = Object.entries(stats.nodes || {});
  if (Number(stats.other?.cpu_pct) > 0 || Number(stats.other?.mem_bytes) > 0) entries.push(["other", stats.other]);
  for (const [id, usage] of entries) {
    const color = id === "other" ? "#9a9a9a" : NODE_COLORS[id] || "#9a9a9a";
    const label = id === "other" ? "Stack other" : NODE_LABELS[id] || id;
    const cpuPct = Math.max(0, Math.min(100, Number(usage?.cpu_pct) || 0));
    const memPct = Math.max(0, Math.min(100, ((Number(usage?.mem_bytes) || 0) / memTotal) * 100));
    if (cpuPct > 0) {
      cpu.push({ id, color, percent: cpuPct, title: `${label} — ${cpuPct.toFixed(1)}% CPU` });
      cpuSum += cpuPct;
    }
    if (memPct > 0) {
      mem.push({ id, color, percent: memPct, title: `${label} — ${formatBytes(usage.mem_bytes)} (${memPct.toFixed(1)}%)` });
      memSum += memPct;
    }
  }
  const idle = Math.max(0, 100 - cpuSum);
  const free = Math.max(0, 100 - memSum);
  cpu.push({ id: "idle", color: "", percent: idle, title: `Idle — ${idle.toFixed(1)}%` });
  mem.push({ id: "free", color: "", percent: free, title: `Free — ${formatBytes((memTotal * free) / 100)} (${free.toFixed(1)}%)` });
  return { cpu, mem };
}

export function arcPath(startFrac, endFrac) {
  if (endFrac - startFrac >= 0.9999) return "M 1 0 A 1 1 0 1 1 1 -0.0002 L 0 0 Z";
  const point = (f) => `${Math.cos(2 * Math.PI * f).toFixed(4)} ${Math.sin(2 * Math.PI * f).toFixed(4)}`;
  const large = endFrac - startFrac > 0.5 ? 1 : 0;
  return `M ${point(startFrac)} A 1 1 0 ${large} 1 ${point(endFrac)} L 0 0 Z`;
}

const SVG_NS = "http://www.w3.org/2000/svg";

function renderPies(stats) {
  const wrap = document.querySelector("#host-stats [data-host-pies]");
  const view = pieView(stats);
  if (!wrap || !view) return;
  wrap.textContent = "";
  for (const slices of [view.cpu, view.mem]) {
    const svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("width", "40");
    svg.setAttribute("height", "40");
    svg.setAttribute("viewBox", "-1.15 -1.15 2.3 2.3");
    let at = 0;
    for (const slice of slices) {
      const frac = Math.max(0, Math.min(1 - at, slice.percent / 100));
      if (frac <= 0) continue;
      const path = document.createElementNS(SVG_NS, "path");
      path.setAttribute("d", arcPath(at, at + frac));
      path.style.fill = slice.color || "var(--glass-track)";
      const title = document.createElementNS(SVG_NS, "title");
      title.textContent = slice.title;
      path.appendChild(title);
      svg.appendChild(path);
      at += frac;
    }
    wrap.appendChild(svg);
  }
  const caption = document.createElement("span");
  caption.textContent = "CPU · Mem";
  wrap.appendChild(caption);
  wrap.hidden = false;
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
  if (host?.stats) renderPies(host.stats);
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
