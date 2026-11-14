import { dashboardFetch as fetch } from "./session.js";

export const NODE_IDS = ["bitcoin", "monero", "tari", "litecoin", "dogecoin", "bitcoincash", "zcash"];
const NODES = new Set(NODE_IDS);

export function normalizeNode(node) {
  return NODES.has(node) ? node : "";
}

export function updateTowerProgress(element, data) {
  if (!element || data?.enabled === false || data?.available !== true) return;
  const progress = data.synced === true ? 100 : data.sync_percent;
  if (typeof progress === "number" && Number.isFinite(progress)) {
    element.dataset.syncPercent = String(Math.max(0, Math.min(100, progress)));
  }
}

export function applyHover(doc, node) {
  doc.body.dataset.hoverNode = normalizeNode(node);
}

export function formatUptime(seconds) {
  const value = Math.max(0, Math.floor(Number(seconds) || 0));
  const days = Math.floor(value / 86400);
  const hours = Math.floor((value % 86400) / 3600);
  const minutes = Math.floor((value % 3600) / 60);
  return [days && `${days}d`, (days || hours) && `${hours}h`, `${minutes}m`].filter(Boolean).join(" ");
}

export function formatBytes(bytes) {
  const value = Number(bytes);
  if (!Number.isFinite(value) || value < 0) return "—";
  return `${(value / (1024 ** 3)).toFixed(2)} GiB`;
}

export function formatCompactBytes(bytes) {
  const value = Number(bytes);
  if (!Number.isFinite(value) || value < 0) return "—";
  if (value < 1024) return `${Math.round(value)} B`;
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / 1024 ** 2).toFixed(1)} MiB`;
}

export function formatHashrate(rate) {
  const value = Number(rate);
  if (!Number.isFinite(value) || value < 0) return "—";
  const units = ["H/s", "kH/s", "MH/s", "GH/s", "TH/s"];
  let scaled = value;
  let unit = 0;
  while (scaled >= 1000 && unit < units.length - 1) { scaled /= 1000; unit++; }
  return `${scaled.toFixed(scaled >= 100 ? 0 : scaled >= 10 ? 1 : 2)} ${units[unit]}`;
}

export function formatMoneroFees(values) {
  if (!Array.isArray(values) || values.length < 4) return "—";
  const fees = values.slice(0, 4).map((value) => Number(value));
  if (fees.some((value) => !Number.isFinite(value) || value < 0)) return "—";
  return fees.map((value, index) => `${["Slow", "Normal", "Fast", "Fastest"][index]}: ${(value / 1e9).toFixed(12).replace(/\.?0+$/, "")}`).join("\n");
}

export function formatUpdated(unixSeconds, DateTimeFormat = Intl.DateTimeFormat) {
  const value = Number(unixSeconds);
  if (!Number.isFinite(value) || value <= 0) return "—";
  try {
    return new DateTimeFormat(undefined, { timeStyle: "medium" }).format(new Date(value * 1000));
  } catch {
    return new Date(value * 1000).toISOString().slice(11, 19);
  }
}

export function moneroView(data) {
  if (!data || data.enabled === false) return { state: "Not configured", badge: "Disabled", details: false };
  if (data.available !== true) return { state: "Connecting...", badge: "Connecting", details: false };
  const heightAvailable = typeof data.height === "number" && Number.isFinite(data.height) && data.height >= 0;
  const height = heightAvailable ? Math.floor(data.height) : 0;
  const target = Math.max(height, Math.floor(Number(data.target_height) || 0));
  const progress = Math.max(0, Math.min(100, Number(data.sync_percent) || 0));
  return {
    state: data.synced ? "Synced" : "Synchronizing the Monero blockchain...",
    badge: data.pruned ? "Pruned" : "Full",
    details: true,
    version: String(data.version || "—"),
    uptime: data.uptime_available ? formatUptime(data.uptime_seconds) : "—",
    connections: `${Math.max(0, Number(data.incoming_connections) || 0)} in · ${Math.max(0, Number(data.outgoing_connections) || 0)} out`,
    blocks: `${height} / ${target}`,
    progress,
    size: data.database_size_available ? formatBytes(data.database_size_bytes) : "—",
    mempool: data.mempool_txs_available ? `${Math.max(0, Math.floor(Number(data.mempool_txs) || 0))} tx${data.mempool_bytes_available ? ` · ${formatCompactBytes(data.mempool_bytes)}` : ""}` : "—",
    difficulty: data.difficulty_available && Number.isFinite(Number(data.difficulty)) ? Math.max(0, Math.floor(Number(data.difficulty))).toLocaleString("en-US") : "—",
    hashrate: data.hashrate_available ? formatHashrate(data.network_hashrate) : "—",
    lastBlock: data.latest_block_available ? `${data.latest_block_age_seconds < 60 ? "<1m" : formatUptime(data.latest_block_age_seconds)} ago` : "—",
    fees: data.fees_available ? formatMoneroFees(data.fee_estimates_atomic_per_byte) : "—",
    rpcExposure: data.rpc_exposure_enabled ? `${String(data.rpc_endpoint || "—")} · LAN` : "Disabled",
    zmqExposure: data.zmq_exposure_enabled ? `${String(data.zmq_endpoint || "—")} · LAN` : "Disabled",
    nextBlock: heightAvailable ? height + 1 : "",
    updated: formatUpdated(data.updated_at),
  };
}

export function genericNodeView(data, label = "Node") {
  if (!data || data.enabled === false) return { state: "Not configured", badge: "Disabled", details: false };
  if (data.available !== true) return { state: `Connecting to ${label}...`, badge: "Connecting", details: false };
  const heightAvailable = typeof data.height === "number" && Number.isFinite(data.height) && data.height >= 0;
  const height = heightAvailable ? Math.floor(data.height) : 0;
  const target = Math.max(height, Math.floor(Number(data.target_height) || 0));
  const progress = Math.max(0, Math.min(100, Number(data.sync_percent) || 0));
  const incoming = Math.max(0, Math.floor(Number(data.incoming_connections) || 0));
  const outgoing = Math.max(0, Math.floor(Number(data.outgoing_connections) || 0));
  const connections = Math.max(0, Math.floor(Number(data.connections) || incoming + outgoing));
  return {
    state: data.synced ? "Synced" : `Synchronizing the ${label} blockchain...`,
    badge: data.pruned_available === false ? "Pruning unknown" : data.pruned ? "Pruned" : "Full",
    details: true,
    version: String(data.version || "—"),
    uptime: data.uptime_available ? formatUptime(data.uptime_seconds) : "—",
    connections: data.connections_available === false ? "—" : incoming || outgoing ? `${incoming} in · ${outgoing} out` : String(connections),
    blocks: `${height} / ${target}`,
    progress,
    size: data.database_size_available ? formatBytes(data.database_size_bytes) : "—",
    mempool: data.mempool_txs_available ? `${Math.max(0, Math.floor(Number(data.mempool_txs) || 0))} tx${data.mempool_bytes_available ? ` · ${formatCompactBytes(data.mempool_bytes)}` : ""}` : "—",
    lastBlock: data.latest_block_available ? `${Number(data.latest_block_age_seconds) < 60 ? "<1m" : formatUptime(data.latest_block_age_seconds)} ago` : "—",
    rpcExposure: data.rpc_exposure_enabled ? `${String(data.rpc_endpoint || "—")} · LAN` : "Disabled",
    nextBlock: heightAvailable ? height + 1 : "",
    updated: formatUpdated(data.updated_at),
    warning: data.end_of_support_warning === true ? endOfSupportWarning(data, label) : "",
  };
}

function endOfSupportWarning(data, label) {
  const cap = Math.floor(Number(data.end_of_support_height)) || 0;
  const left = Math.floor(Number(data.end_of_support_remaining_blocks)) || 0;
  const when = left > 0 ? `in ${left.toLocaleString("en-US")} blocks` : "now";
  return `${label} stops after block ${cap.toLocaleString("en-US")} (${when}). Upgrade node-manager once a newer Zakura is pinned.`;
}

export function applySelection(doc, node, focus = true) {
  const selected = normalizeNode(node);
  doc.body.dataset.node = selected;
  for (const panel of doc.querySelectorAll("[data-node-panel]")) {
    panel.hidden = panel.dataset.nodePanel !== selected;
  }
  for (const choice of doc.querySelectorAll("[data-select-node]")) {
    choice.setAttribute("aria-expanded", String(choice.dataset.selectNode === selected));
  }
  if (!focus) return selected;
  const target = selected
    ? doc.querySelector(`[data-node-panel="${selected}"] [data-close-node]`)
    : doc.querySelector("[data-select-node]:not([hidden])");
  target?.focus({ preventScroll: true });
  return selected;
}

export function selectionController(doc, onChange = () => {}) {
  let lastChoice = null;
  const resetScroll = () => {
    doc.defaultView?.scrollTo(0, 0);
    for (const root of [doc.scrollingElement, doc.documentElement, doc.body]) {
      if (root) root.scrollTop = root.scrollLeft = 0;
    }
    const overview = doc.getElementById("node-overview");
    if (overview) overview.scrollTop = 0;
  };
  return {
    open(node, choice) {
      lastChoice = choice || lastChoice;
      const selected = applySelection(doc, node);
      resetScroll();
      onChange(selected);
      return selected;
    },
    close() {
      applySelection(doc, "", false);
      lastChoice?.focus({ preventScroll: true });
      resetScroll();
      onChange("");
    },
    refresh() {
      return applySelection(doc, doc.body.dataset.node, false);
    },
  };
}

if (typeof document !== "undefined") {
  const controller = selectionController(document, (node) => {
    window.dispatchEvent(new CustomEvent("node-selected", { detail: { node } }));
  });

  document.addEventListener("click", (event) => {
    const choice = event.target.closest?.("[data-select-node]");
    if (choice) controller.open(choice.dataset.selectNode, choice);
    else if (event.target.closest?.("[data-close-node]") || event.target.id === "node-close-backdrop") controller.close();
  });
  document.addEventListener("pointerover", (event) => {
    const choice = event.target.closest?.("[data-select-node]");
    if (choice) applyHover(document, choice.dataset.selectNode);
  });
  document.addEventListener("pointerout", (event) => {
    const choice = event.target.closest?.("[data-select-node]");
    if (choice && !choice.contains(event.relatedTarget) && !choice.contains(document.activeElement)) applyHover(document, "");
  });
  document.addEventListener("focusin", (event) => {
    const choice = event.target.closest?.("[data-select-node]");
    if (choice) applyHover(document, choice.dataset.selectNode);
  });
  document.addEventListener("focusout", (event) => {
    const choice = event.target.closest?.("[data-select-node]");
    if (choice && !choice.contains(event.relatedTarget)) applyHover(document, "");
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && document.body.dataset.node && !document.getElementById("settings-dialog")?.open) controller.close();
  });
  window.addEventListener("live-updated", () => controller.refresh());

  const renderMonero = (data) => {
    const view = moneroView(data);
    document.getElementById("monero-badge").textContent = view.badge;
    document.getElementById("monero-state").textContent = view.state;
    const details = document.getElementById("monero-details");
    details.hidden = !view.details;
    const live = document.getElementById("monero-live");
    live.dataset.nextBlock = view.nextBlock || "";
    updateTowerProgress(live, data);
    if (!view.details) return;
    if (data.database_size_available) window.dispatchEvent(new CustomEvent("node-size", { detail: { id: "monero", bytes: Number(data.database_size_bytes) || 0 } }));
    document.getElementById("monero-version").textContent = view.version;
    document.getElementById("monero-uptime").textContent = view.uptime;
    document.getElementById("monero-connections").textContent = view.connections;
    document.getElementById("monero-blocks").textContent = view.blocks;
    document.getElementById("monero-progress").style.width = `${view.progress}%`;
    document.getElementById("monero-sync").textContent = `${view.progress.toFixed(4)}%`;
    document.getElementById("monero-size").textContent = view.size;
    document.getElementById("monero-mempool").textContent = view.mempool;
    document.getElementById("monero-difficulty").textContent = view.difficulty;
    document.getElementById("monero-hashrate").textContent = view.hashrate;
    document.getElementById("monero-last-block").textContent = view.lastBlock;
    document.getElementById("monero-fees").textContent = view.fees;
    document.getElementById("monero-rpc-exposure").textContent = view.rpcExposure;
    document.getElementById("monero-zmq-exposure").textContent = view.zmqExposure;
    document.getElementById("monero-live").dataset.nextBlock = view.nextBlock;
    document.getElementById("monero-updated").textContent = `Updated: ${view.updated}`;
  };

  async function refreshMonero() {
    try {
      const response = await fetch("/api/monero", { headers: { "X-Requested-With": "fetch" } });
      if (!response.ok) throw new Error("unavailable");
      renderMonero(await response.json());
    } catch {
      renderMonero({ enabled: true, available: false });
    }
  }

  applySelection(document, "", false);
  window.addEventListener("node-power-updated", (event) => {
    if (event.detail?.id === "monero" && event.detail?.active === true) refreshMonero();
  });
  refreshMonero();
  setInterval(refreshMonero, 30000);
}
