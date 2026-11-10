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

function nodeSlice(id, bytes, percent, suffix) {
  return {
    id,
    color: NODE_COLORS[id] || "#9a9a9a",
    percent,
    title: `${NODE_LABELS[id] || id} — ${formatBytes(bytes)} (${percent.toFixed(2)}%${suffix})`,
  };
}

function barSlices(totalBytes, freeBytes, sizes, nodeFilter) {
  const pct = (bytes) => (bytes / totalBytes) * 100;
  const used = totalBytes - freeBytes;
  const slices = [];
  let accounted = 0;
  for (const [id, bytes] of sizes) {
    if (nodeFilter && !nodeFilter.includes(id)) continue;
    const value = Math.floor(Number(bytes));
    if (!(value > 0)) continue;
    accounted += value;
    slices.push(nodeSlice(id, value, pct(value), ""));
  }
  const other = Math.max(0, used - accounted);
  slices.push({ id: "other", color: "#9a9a9a", percent: pct(other), title: `Other data — ${formatBytes(other)} (${pct(other).toFixed(2)}%)` });
  slices.push({ id: "free", color: "transparent", percent: pct(freeBytes), title: `Free — ${formatBytes(freeBytes)} (${pct(freeBytes).toFixed(2)}%)` });
  return slices;
}

export function diskViews(host, sizes) {
  if (host?.disk_available !== true) return null;
  let disks = Array.isArray(host.disks) ? host.disks : [];
  disks = disks.filter((disk) => Number(disk?.disk_total_bytes) > 0);
  if (disks.length === 0) disks = [{ disk_total_bytes: host.disk_total_bytes, disk_free_bytes: host.disk_free_bytes, nodes: null }];
  let total = 0;
  let free = 0;
  const bars = [];
  for (const [index, disk] of disks.entries()) {
    const diskTotal = Number(disk.disk_total_bytes);
    if (!(diskTotal > 0)) continue;
    const diskFree = Math.min(Math.max(0, Number(disk.disk_free_bytes) || 0), diskTotal);
    total += diskTotal;
    free += diskFree;
    const usedPct = ((diskTotal - diskFree) / diskTotal) * 100;
    bars.push({
      title: `Disk ${index + 1} — ${usedPct.toFixed(0)}% used · ${formatBytes(diskTotal - diskFree)} of ${formatBytes(diskTotal)}`,
      slices: barSlices(diskTotal, diskFree, sizes, Array.isArray(disk.nodes) ? disk.nodes : null),
    });
  }
  if (bars.length === 0 || !(total > 0)) return null;
  const usedPct = ((total - free) / total) * 100;
  return {
    line: `Disk${bars.length > 1 ? "s" : ""} ${usedPct.toFixed(0)}% used`,
    lineTitle: `${formatBytes(total - free)} of ${formatBytes(total)} used · ${formatBytes(free)} free`,
    bars,
  };
}

export function diskZoomView(sizes) {
  let total = 0;
  for (const [, bytes] of sizes) total += Math.max(0, Math.floor(Number(bytes)) || 0);
  if (!(total > 0)) return null;
  const slices = [];
  for (const [id, bytes] of sizes) {
    const value = Math.floor(Number(bytes));
    if (!(value > 0)) continue;
    slices.push(nodeSlice(id, value, (value / total) * 100, " of node data"));
  }
  return {
    line: `Node data ${formatBytes(total)}`,
    lineTitle: `${formatBytes(total)} across ${slices.length} node${slices.length === 1 ? "" : "s"}`,
    bars: [{ title: "Node-manager data only", slices }],
  };
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
  const cpuUsed = Math.min(100, cpuSum);
  const memUsed = Math.min(100, memSum);
  return {
    cpu,
    mem,
    cpuLabel: `CPU ${cpuUsed.toFixed(0)}%`,
    cpuLabelTitle: `${((cpuUsed / 100) * cpus).toFixed(1)} of ${cpus} cores`,
    memLabel: `Mem ${memUsed.toFixed(0)}%`,
    memLabelTitle: `${((memTotal * memUsed) / 100 / 1024 ** 3).toFixed(1)} of ${(memTotal / 1024 ** 3).toFixed(1)} GiB`,
  };
}

export function pieZoomView(stats) {
  const base = pieView(stats);
  if (!base) return null;
  const rescale = (slices) => {
    const used = slices.filter((slice) => slice.id !== "idle" && slice.id !== "free");
    const total = used.reduce((sum, slice) => sum + slice.percent, 0);
    if (!(total > 0)) return null;
    return used.map((slice) => ({ ...slice, percent: (slice.percent / total) * 100, title: `${slice.title} · ${((slice.percent / total) * 100).toFixed(1)}% of stack` }));
  };
  const cpu = rescale(base.cpu);
  const mem = rescale(base.mem);
  if (!cpu || !mem) return null;
  return { cpu, mem, cpuLabel: base.cpuLabel, cpuLabelTitle: base.cpuLabelTitle, memLabel: base.memLabel, memLabelTitle: base.memLabelTitle };
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
  const view = (piesZoomed && pieZoomView(stats)) || pieView(stats);
  if (!wrap || !view) return;
  wrap.title = piesZoomed ? "Showing node-manager usage only — click to zoom out" : "Click to zoom into node-manager usage";
  wrap.textContent = "";
  for (const [slices, label, labelTitle] of [[view.cpu, view.cpuLabel, view.cpuLabelTitle], [view.mem, view.memLabel, view.memLabelTitle]]) {
    const svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("width", "52");
    svg.setAttribute("height", "52");
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
    const figure = document.createElement("figure");
    const caption = document.createElement("figcaption");
    caption.textContent = label;
    if (labelTitle) caption.title = labelTitle;
    figure.appendChild(svg);
    figure.appendChild(caption);
    wrap.appendChild(figure);
  }
  wrap.hidden = false;
}

const sizes = new Map();
let host = null;
let zoomed = false;
let piesZoomed = false;

function render() {
  const panel = document.getElementById("host-stats");
  if (!panel) return;
  const bitcoinBytes = Number(document.getElementById("live")?.dataset.nodeSize);
  if (bitcoinBytes > 0) sizes.set("bitcoin", bitcoinBytes);
  const view = (zoomed && diskZoomView(sizes)) || diskViews(host, sizes);
  if (!view) return;
  const line = panel.querySelector("[data-host-line]");
  line.textContent = view.line;
  const hint = zoomed ? "click a bar to zoom out" : "click a bar to zoom into node-manager data";
  line.title = view.lineTitle ? `${view.lineTitle} — ${hint}` : hint;
  const wrap = panel.querySelector("[data-host-disk]");
  wrap.textContent = "";
  for (const bar of view.bars) {
    const row = document.createElement("div");
    row.className = "host-stats-bar";
    row.title = bar.title;
    for (const slice of bar.slices) {
      const part = document.createElement("div");
      part.style.width = `${Math.max(0, Math.min(100, slice.percent))}%`;
      part.style.background = slice.color;
      part.title = slice.title;
      row.appendChild(part);
    }
    wrap.appendChild(row);
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
  document.querySelector("#host-stats [data-host-disk]")?.addEventListener("click", () => {
    zoomed = !zoomed;
    render();
  });
  document.querySelector("#host-stats [data-host-pies]")?.addEventListener("click", () => {
    piesZoomed = !piesZoomed;
    render();
  });
}
