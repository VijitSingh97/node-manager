import { formatBytes } from "./nodes.js";
import { NODE_LABELS } from "./chains.js";

export const OTHER_COLOR = "#6e6e6e";

export const NODE_COLORS = {
  stack: "#2a9d8f",
  bitcoin: "#f2a900",
  monero: "#ff6600",
  tari: "#8f5cff",
  litecoin: "#4d8fd8",
  dogecoin: "#b88746",
  bitcoincash: "#36a852",
  zcash: "#d69c35",
};

function sliceLabel(id) {
  if (id === "stack") return "Node Manager itself";
  return NODE_LABELS[id] || id;
}

function nodeSlice(id, bytes, percent, suffix) {
  return {
    id,
    color: NODE_COLORS[id] || "#9a9a9a",
    percent,
    title: `${sliceLabel(id)} — ${formatBytes(bytes)} (${percent.toFixed(2)}%${suffix})`,
  };
}

function nodeBytes(sizes, nodeFilter) {
  const entries = [];
  for (const [id, bytes] of sizes) {
    if (nodeFilter && !nodeFilter.includes(id)) continue;
    const value = Math.floor(Number(bytes));
    if (value > 0) entries.push([id, value]);
  }
  return entries;
}

function overviewSlices(totalBytes, freeBytes, sizes, nodeFilter) {
  const pct = (bytes) => Math.min(100, (bytes / totalBytes) * 100);
  const used = totalBytes - freeBytes;
  const slices = [];
  let accounted = 0;
  for (const [id, value] of nodeBytes(sizes, nodeFilter)) {
    accounted += value;
    slices.push(nodeSlice(id, value, pct(value), ""));
  }
  const other = Math.max(0, used - accounted);
  slices.push({ id: "other", color: OTHER_COLOR, percent: pct(other), title: `Other data — ${formatBytes(other)} (${pct(other).toFixed(2)}%)` });
  slices.push({ id: "free", color: "transparent", percent: pct(freeBytes), title: `Free — ${formatBytes(freeBytes)} (${pct(freeBytes).toFixed(2)}%)` });
  return slices;
}

export function zoomSlices(sizes, nodeFilter, diskTotalBytes = 0) {
  const entries = nodeBytes(sizes, nodeFilter);
  const total = entries.reduce((sum, [, value]) => sum + value, 0);
  if (!(total > 0)) return null;
  return entries.map(([id, value]) => {
    const slice = nodeSlice(id, value, (value / total) * 100, " of Node Manager data");
    if (diskTotalBytes > 0) {
      slice.title = `${sliceLabel(id)} — ${formatBytes(value)} (${Math.min(100, (value / diskTotalBytes) * 100).toFixed(2)}% of disk)`;
    }
    return slice;
  });
}

export function withCollectorSizes(stats, sizes) {
  const merged = new Map();
  for (const [id, usage] of Object.entries(stats?.nodes || {})) {
    const bytes = Number(usage?.disk_bytes);
    if (bytes > 0) merged.set(id, bytes);
  }
  for (const [id, bytes] of sizes) {
    if (Number(bytes) > 0) merged.set(id, Number(bytes));
  }
  return merged;
}

export function diskViews(host, sizes, zoomedDisks = new Set()) {
  if (host?.disk_available !== true) return null;
  let disks = Array.isArray(host.disks) ? host.disks : [];
  disks = disks.filter((disk) => Number(disk?.disk_total_bytes) > 0);
  const occupied = disks.filter((disk) => !Array.isArray(disk.nodes) || disk.nodes.some((id) => Number(sizes.get(id)) > 0));
  if (occupied.length > 0) disks = occupied;
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
    const nodeFilter = Array.isArray(disk.nodes) ? disk.nodes : null;
    const usedPct = ((diskTotal - diskFree) / diskTotal) * 100;
    const zoom = zoomedDisks.has(index) ? zoomSlices(sizes, nodeFilter, diskTotal) : null;
    const device = typeof disk.name === "string" && disk.name !== "" ? disk.name : `Disk ${index + 1}`;
    const name = typeof disk.label === "string" && disk.label !== "" ? disk.label : device;
    bars.push({
      index,
      label: name,
      zoomed: zoom !== null,
      title: zoom
        ? `${name} — Node Manager data only, click to zoom out`
        : `${name} — ${usedPct.toFixed(0)}% used · ${formatBytes(diskTotal - diskFree)} of ${formatBytes(diskTotal)}`,
      slices: zoom || overviewSlices(diskTotal, diskFree, sizes, nodeFilter),
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

export function rescaleToStack(slices) {
  const used = slices.filter((slice) => slice.id !== "idle" && slice.id !== "free");
  const total = used.reduce((sum, slice) => sum + slice.percent, 0);
  if (!(total > 0)) return null;
  return used.map((slice) => ({ ...slice, percent: (slice.percent / total) * 100, title: `${slice.title} · ${((slice.percent / total) * 100).toFixed(1)}% of stack` }));
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
    const color = id === "other" ? OTHER_COLOR : NODE_COLORS[id] || "#9a9a9a";
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

export function arcPath(startFrac, endFrac) {
  if (endFrac - startFrac >= 0.9999) return "M 1 0 A 1 1 0 1 1 1 -0.0002 L 0 0 Z";
  const point = (f) => `${Math.cos(2 * Math.PI * f).toFixed(4)} ${Math.sin(2 * Math.PI * f).toFixed(4)}`;
  const large = endFrac - startFrac > 0.5 ? 1 : 0;
  return `M ${point(startFrac)} A 1 1 0 ${large} 1 ${point(endFrac)} L 0 0 Z`;
}

const SVG_NS = "http://www.w3.org/2000/svg";

function buildPie(slices, label, labelTitle, zoomedFlag, onToggle) {
  const svg = document.createElementNS(SVG_NS, "svg");
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
  figure.title = zoomedFlag ? "Node-manager usage only — click to zoom out" : "Click to zoom into node-manager usage";
  const caption = document.createElement("figcaption");
  caption.textContent = label;
  if (labelTitle) caption.title = labelTitle;
  figure.appendChild(svg);
  figure.appendChild(caption);
  figure.addEventListener("click", onToggle);
  return figure;
}

function renderPies(stats) {
  const wrap = document.querySelector("#host-stats [data-host-pies]");
  const view = pieView(stats);
  if (!wrap || !view) return;
  wrap.textContent = "";
  const cpuSlices = (cpuZoomed && rescaleToStack(view.cpu)) || view.cpu;
  const memSlices = (memZoomed && rescaleToStack(view.mem)) || view.mem;
  const cpuLabel = cpuZoomed ? view.cpuLabel.replace("CPU", "Node Manager CPU") : view.cpuLabel;
  const memLabel = memZoomed ? view.memLabel.replace("Mem", "Node Manager Mem") : view.memLabel;
  wrap.appendChild(buildPie(cpuSlices, cpuLabel, view.cpuLabelTitle, cpuZoomed, () => {
    cpuZoomed = !cpuZoomed;
    render();
  }));
  wrap.appendChild(buildPie(memSlices, memLabel, view.memLabelTitle, memZoomed, () => {
    memZoomed = !memZoomed;
    render();
  }));
  wrap.hidden = false;
}

const sizes = new Map();
let host = null;
const zoomedDisks = new Set();
let cpuZoomed = false;
let memZoomed = false;

function render() {
  const panel = document.getElementById("host-stats");
  if (!panel) return;
  const bitcoinBytes = Number(document.getElementById("live")?.dataset.nodeSize);
  if (bitcoinBytes > 0) sizes.set("bitcoin", bitcoinBytes);
  const view = diskViews(host, withCollectorSizes(host?.stats, sizes), zoomedDisks);
  if (!view) return;
  const line = panel.querySelector("[data-host-line]");
  line.textContent = view.line;
  line.title = `${view.lineTitle} — click a bar or pie to zoom into node-manager usage`;
  const wrap = panel.querySelector("[data-host-disk]");
  wrap.textContent = "";
  for (const bar of view.bars) {
    if (view.bars.length > 1) {
      const label = document.createElement("div");
      label.className = "host-stats-bar-label";
      label.textContent = `${bar.label}${bar.zoomed ? " · Node Manager data" : ""}`;
      wrap.appendChild(label);
    }
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
    row.addEventListener("click", () => {
      if (zoomedDisks.has(bar.index)) zoomedDisks.delete(bar.index);
      else zoomedDisks.add(bar.index);
      render();
    });
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
}
