import { actuatorCurlController } from "./actuator-curl.js";
import { credentialSettingsController } from "./credentials.js";
import { dashboardFetch as fetch } from "./session.js";
import { askConfirm } from "./confirm-dialog.js";

const extraNodes = ["tari", "litecoin", "dogecoin", "bitcoincash", "zcash"];
const allNodes = ["bitcoin", "monero", ...extraNodes];
const allPortIDs = ["bitcoin_rpc", "monero_rpc", "monero_zmq", "tari_grpc", "litecoin_rpc", "dogecoin_rpc", "bitcoincash_rpc", "zcash_rpc"];
const nodePortIDs = {
  bitcoin: ["bitcoin_rpc"], monero: ["monero_rpc", "monero_zmq"], tari: ["tari_grpc"],
  litecoin: ["litecoin_rpc"], dogecoin: ["dogecoin_rpc"], bitcoincash: ["bitcoincash_rpc"], zcash: ["zcash_rpc"],
};
const globalAlertDefaults = {
  alert_monitor_online: true,
  alert_node_down: true,
  alert_node_recovered: true,
  alert_sync_complete: true,
  alert_low_disk: true,
  alert_updates: true,
  node_down_after_minutes: 3,
  low_disk_gib: 50,
};

function boundedInteger(value, fallback, minimum, maximum) {
  const number = Number(value);
  return Number.isInteger(number) && number >= minimum && number <= maximum ? number : fallback;
}

export function alertPreferenceModel(data = {}) {
  return {
    alert_monitor_online: data.alert_monitor_online !== false,
    alert_node_down: data.alert_node_down !== false,
    alert_node_recovered: data.alert_node_recovered !== false,
    alert_sync_complete: data.alert_sync_complete !== false,
    alert_low_disk: data.alert_low_disk !== false,
    alert_updates: data.alert_updates !== false,
    node_down_after_minutes: boundedInteger(data.node_down_after_minutes, globalAlertDefaults.node_down_after_minutes, 1, 60),
    low_disk_gib: boundedInteger(data.low_disk_gib, globalAlertDefaults.low_disk_gib, 1, 1048576),
  };
}

export function notificationPayload(values) {
  const payload = {
    telegram_enabled: !!values.telegram_enabled,
    telegram_chat_id: values.telegram_enabled ? String(values.telegram_chat_id || "").trim() : "",
    healthchecks_enabled: !!values.healthchecks_enabled,
    alert_new_block: !!values.alert_new_block_bitcoin,
    alert_new_block_monero: !!values.alert_new_block_monero,
    tower_labels: !!values.tower_labels,
    computer_stats: !!values.computer_stats,
    thermal_throttle: !!values.thermal_throttle,
    default_currency: normalizeDefaultCurrency(values.default_currency),
    actuator_health: !!values.actuator_health,
    actuator_info: !!values.actuator_info,
    ...alertPreferenceModel(values),
  };
  for (const node of extraNodes) payload[`alert_new_block_${node}`] = !!values[`alert_new_block_${node}`];
  const token = String(values.telegram_bot_token || "").trim();
  const url = String(values.healthchecks_url || "").trim();
  if (payload.telegram_enabled && token) payload.telegram_bot_token = token;
  if (payload.healthchecks_enabled && url) payload.healthchecks_url = url;
  return payload;
}

export function configurationAccess(data) {
  return data?.can_configure === true;
}

const networkActions = ["clearnet_over_tor", "inbound_connections"];
const balanceActions = ["utxo_index", "utxo_seed"];
export const UTXO_INDEX_LITECOIN_OFF_PROMPT = "Turn off instant Litecoin balances? Wallets go back to the slower scan. The index volume stays on disk until you run utxo-index remove litecoin.";
export const UTXO_INDEX_DOGECOIN_OFF_PROMPT = "Turn off instant Dogecoin balances? Wallets go back to the archival watch wallet if it is on, otherwise they wait. The index volume stays on disk until you run utxo-index remove dogecoin.";
export const UTXO_INDEX_BITCOINCASH_OFF_PROMPT = "Turn off instant Bitcoin Cash balances? Wallets go back to the slower scan. The index volume stays on disk until you run utxo-index remove bitcoincash.";
const chainIndexOffPrompts = { litecoin: UTXO_INDEX_LITECOIN_OFF_PROMPT, bitcoincash: UTXO_INDEX_BITCOINCASH_OFF_PROMPT, dogecoin: UTXO_INDEX_DOGECOIN_OFF_PROMPT };
const chainIndexNode = (action) => String(action || "").startsWith("utxo_index_") && chainIndexOffPrompts[action.slice(11)] ? action.slice(11) : "";
const toggleKey = (change) => change?.action === "utxo_index" && chainIndexOffPrompts[change?.node] ? `utxo_index_${change.node}` : change?.action;
const toggleFields = { launch_at_boot: "boot_enabled", clearnet_over_tor: "clearnet_over_tor", inbound_connections: "inbound_connections", utxo_index: "utxo_index", utxo_index_litecoin: "utxo_index_litecoin", utxo_index_bitcoincash: "utxo_index_bitcoincash", utxo_index_dogecoin: "utxo_index_dogecoin" };
export const hostToggleDefaults = { launch_at_boot: false, clearnet_over_tor: true, inbound_connections: true, utxo_index: false, utxo_index_litecoin: false, utxo_index_bitcoincash: false, utxo_index_dogecoin: false };
export const UTXO_INDEX_OFF_PROMPT = "Turn off instant Bitcoin balances? Wallets go back to the slower scan. The index volume stays on disk until you run utxo-index remove.";

export async function balanceIndexPayload(enabled, confirmFn) {
  if (enabled === false && !(await confirmFn(UTXO_INDEX_OFF_PROMPT))) return undefined;
  return hostActionPayload("utxo_index", enabled);
}

export function hostToggleChanges(view, draft) {
  return Object.keys(toggleFields)
    .filter((action) => typeof draft?.[action] === "boolean" && draft[action] !== view?.[toggleFields[action]])
    .map((action) => hostActionPayload(action, draft[action]));
}

export function savedHostActions(view, draft, staged = {}) {
  const byAction = (action) => Object.values(staged).filter((payload) => payload?.action === action);
  return [...byAction("apply_config"), ...hostToggleChanges(view, draft), ...byAction("install"), ...byAction("migrate"), ...byAction("delete")];
}

export async function confirmedHostChanges(changes, confirmFn) {
  const confirmed = [];
  for (const change of changes) {
    const prompt = change.action === "utxo_index" && chainIndexOffPrompts[change.node];
    const kept = change.action !== "utxo_index" ? change
      : prompt ? (change.enabled === false && !(await confirmFn(prompt)) ? undefined : change)
      : await balanceIndexPayload(change.enabled, confirmFn);
    if (kept) confirmed.push(kept);
  }
  return confirmed;
}

const sameHostChange = (status, change) => status?.action === change?.action && (status?.node || "") === (change?.node || "") && (!change?.requestId || !status?.id || status.id === change.requestId);

export function hostQueueSettled(view, change) {
  if (!change || view?.status?.state === "pending") return "";
  return sameHostChange(view?.status, change) && ["rejected", "failed"].includes(view.status.state) ? "failed" : "applied";
}

export function hostChangeLabel(change, nodes = {}) {
  const name = (id) => nodes[id]?.name || id || "Bitcoin";
  const labels = { install: `Install ${name(change?.node)}`, migrate: `Move ${name(change?.node)} data`, delete: `Delete ${name(change?.node)}`, utxo_index: `${name(change?.node)} balance index`,
    launch_at_boot: "Launch at start", apply_config: "New configuration", clearnet_over_tor: "Reach clearnet through Tor", inbound_connections: "Incoming connections" };
  return labels[change?.action] || "Host change";
}

// A failed change only holds back later changes to the same node.
export function splitDependentChanges(queue, failed) {
  const nodeOf = (change) => change.node || (change.action === "utxo_index" ? "bitcoin" : "");
  const dependent = (change) => !!failed && !!nodeOf(failed) && nodeOf(change) === nodeOf(failed);
  return { rest: queue.filter((change) => !dependent(change)), skipped: queue.filter(dependent) };
}

export function hostQueueSummary(failures, skipped) {
  if (!failures.length) return "Settings saved.";
  return `${failures.map((item) => `${item.label}: ${item.message}`).join(" ")} Skipped: ${skipped.length ? skipped.join(", ") : "none"}.`;
}

export function balanceIndexControl(view, busy) {
  return { hidden: !view.available || !view.nodes.bitcoin.installed, checked: view.utxo_index, disabled: !view.available || view.config_pending || busy };
}

const deleteVolumes = {
  bitcoin: "the balance index volume (utxo_index)",
  litecoin: "the balance index volume (utxo_index_litecoin)",
  bitcoincash: "the balance index volume (utxo_index_bitcoincash)",
  dogecoin: "the balance index volume (utxo_index_dogecoin)",
  monero: "the wallet volume (monero_wallets)",
  tari: "the wallet volumes (tari_wallet, tari_wallet_data)",
};

export function deleteControl(view, node, staged = {}) {
  const reason = !view?.available || !view.delete_supported ? "Deleting needs the installed host helper."
    : view.config_pending ? "Run the new config first."
    : view.status?.state === "pending" ? "Another host change is running."
    : !node?.installed ? "This node is not installed."
    : node.active ? "Deactivate the node first (node settings)."
    : node.last_enabled || Object.values(view.nodes).filter((other) => other.installed && !staged[`delete:${other.id}`]).length <= 1 ? "At least one node must stay installed."
    : !node.data_dir ? "The data folder is not known yet."
    : staged[`migrate:${node.id}`] ? "Undo the staged data move first."
    : "";
  return { disabled: reason !== "", reason };
}

export function deleteImpact(node) {
  const items = [
    `the data folder ${node.data_dir}, including everything in it`,
    ...(deleteVolumes[node.id] ? [deleteVolumes[node.id]] : []),
    `its configuration and credentials for ${node.name}`,
    `the watch-only wallets you added for ${node.name}, with their saved balances and history`,
  ];
  return items;
}

export function deleteConfirmed(node, typed) {
  return !!node?.id && typed === node.id;
}

export function hostControlModel(data = {}) {
  const nodes = Object.fromEntries(allNodes.map((id) => [id, { id, name: id, installed: false, active: false, last_enabled: false, data_dir: "", disk_estimate: "", disk_note: "", unsupported: "", can_index: false, index_refusal: "", interfaces: [] }]));
  for (const node of Array.isArray(data.nodes) ? data.nodes : []) {
    if (!allNodes.includes(node?.id)) continue;
    const interfaces = (Array.isArray(node.interfaces) ? node.interfaces : []).flatMap((item) => {
      const hostPort = Number(item?.host_port);
      if (!nodePortIDs[node.id].includes(item?.id) || !Number.isInteger(hostPort) || hostPort < 1 || hostPort > 65535) return [];
      return [{ id: item.id, name: String(item.name || item.id), enabled: false, host_port: hostPort }];
    });
    nodes[node.id] = {
      id: node.id,
      name: String(node.name || node.id),
      installed: node.installed === true,
      active: node.active === true,
      last_enabled: node.last_enabled === true,
      data_dir: typeof node.data_dir === "string" && node.data_dir.startsWith("/") ? node.data_dir : "",
      disk_estimate: String(node.disk_estimate || ""),
      disk_note: String(node.disk_note || ""),
      unsupported: String(node.unsupported || ""),
      can_index: node.can_index === true,
      index_refusal: String(node.index_refusal || ""),
      interfaces,
    };
  }
  const action = ["install", "migrate", "delete", "launch_at_boot", "apply_config", ...balanceActions, ...networkActions].includes(data?.status?.action) ? data.status.action : "";
  const state = ["pending", "applied", "rejected", "failed"].includes(data?.status?.state) ? data.status.state : "";
  const bootKind = ["boot", "login", "unsupported"].includes(data.boot_kind) ? data.boot_kind : "unsupported";
  const revision = data.config_pending === true && /^[0-9a-f]{64}$/.test(String(data.config_revision || "")) ? data.config_revision : "";
  return {
    available: data.available === true,
    install_supported: data.install_supported === true,
    migrate_supported: data.migrate_supported === true,
    delete_supported: data.delete_supported === true,
    boot_supported: data.boot_supported === true && bootKind !== "unsupported",
    boot_enabled: data.boot_enabled === true,
    boot_kind: bootKind,
    clearnet_over_tor: data.clearnet_over_tor === true,
    inbound_connections: data.inbound_connections === true,
    install_index_supported: data.install_index_supported === true,
    utxo_index: data.utxo_index === true,
    utxo_index_litecoin: data.utxo_index_litecoin === true,
    utxo_index_bitcoincash: data.utxo_index_bitcoincash === true,
    utxo_index_dogecoin: data.utxo_index_dogecoin === true,
    config_pending: !!revision,
    config_revision: revision,
    config_path: typeof data.config_path === "string" && data.config_path.startsWith("/") ? data.config_path : "",
    nodes,
    status: { action, state, id: /^[0-9a-f]{32}$/.test(String(data?.status?.id || "")) ? data.status.id : "", node: allNodes.includes(data?.status?.node) ? data.status.node : "", message: state ? [data.status?.message, data.status?.detail].filter(Boolean).map(String).join(" ") : "" },
  };
}

export function pendingHostNode(view, action) {
  return view?.status?.state === "pending" && view.status.action === action ? view.status.node : "";
}

export function newlyInstalledNodes(previous, current) {
  if (!previous) return [];
  return allNodes.filter((id) => previous.nodes[id]?.installed !== true && current.nodes[id]?.installed === true);
}

export function shouldPollHost(view) {
  return view?.status?.state === "pending";
}

export function latestRequestGate() {
  let generation = 0;
  return {
    invalidate() { generation += 1; },
    async run(request, success, failure) {
      const current = ++generation;
      try {
        const value = await request();
        if (current !== generation) return false;
        success(value);
      } catch (error) {
        if (current !== generation) return false;
        failure(error);
      }
      return true;
    },
  };
}

export function hostActionPayload(action, value, options) {
  if (action === "install" && allNodes.includes(value)) {
    if (options === undefined) return { action, node: value };
    const dataDir = String(options?.data_dir || "");
    const interfaces = Array.isArray(options?.interfaces) ? options.interfaces : [];
    if (!dataDir.startsWith("/") || dataDir.includes("\n") || dataDir.includes("\r") || interfaces.some((item) =>
      !nodePortIDs[value].includes(item?.id) || typeof item.enabled !== "boolean" || !Number.isInteger(item.host_port) || item.host_port < 1 || item.host_port > 65535)) return undefined;
    if (typeof options?.utxo_index === "boolean") return { action, node: value, data_dir: dataDir, interfaces, utxo_index: options.utxo_index };
    return { action, node: value, data_dir: dataDir, interfaces };
  }
  if (action === "migrate" && allNodes.includes(value)) {
    const dataDir = String(options?.data_dir || "");
    if (!dataDir.startsWith("/") || dataDir.length < 2 || dataDir.includes("\n") || dataDir.includes("\r")) return undefined;
    return { action, node: value, data_dir: dataDir };
  }
  if (action === "delete" && allNodes.includes(value) && options?.confirmation === value) return { action, node: value, confirmation: value };
  if (chainIndexNode(action) && typeof value === "boolean") return { action: "utxo_index", node: chainIndexNode(action), enabled: value };
  if (toggleFields[action] && typeof value === "boolean") return { action, enabled: value };
  if (action === "apply_config" && /^[0-9a-f]{64}$/.test(String(value || ""))) return { action, config_revision: value };
  return undefined;
}

export function bootControlLabel(kind) {
  return kind === "login" ? "Launch at login" : "Launch at boot";
}

export function notificationChanged(saved, payload) {
  const savedAlertPreferences = alertPreferenceModel(saved);
  return Object.entries(payload).some(([field, value]) => {
    const previous = field === "alert_new_block" ? saved?.alert_new_block_bitcoin ?? saved?.alert_new_block
      : field === "default_currency" ? normalizeDefaultCurrency(saved?.default_currency)
      : Object.hasOwn(globalAlertDefaults, field) ? savedAlertPreferences[field]
      : saved?.[field];
    return value !== previous;
  });
}

export function settingsEndpoint(scope = "global", method = "GET") {
  return scope === "global" ? (method === "GET" ? "/api/settings" : "/api/settings?scope=global") : "/api/settings?scope=nodes";
}

export function portStatusNode(port, submittedNode = "", changedNodes = []) {
  return Object.keys(nodePortIDs).find((node) => nodePortIDs[node].includes(port))
    || (allNodes.includes(submittedNode) ? submittedNode : "")
    || changedNodes.find((node) => allNodes.includes(node))
    || "";
}

export function scopedSettingsPayload(scope, values) {
  if (scope !== "global") {
    const field = scope === "bitcoin" ? "alert_new_block" : `alert_new_block_${scope}`;
    return { [field]: values?.[field] === true };
  }
  const payload = notificationPayload(values || {});
  for (const field of Object.keys(payload)) if (field.startsWith("alert_new_block")) delete payload[field];
  return payload;
}

export function resetSaveRequests(defaults, payload) {
  const restored = scopedSettingsPayload("global", defaults);
  const edited = Object.keys({ ...restored, ...payload }).some((field) => restored[field] !== payload[field]);
  return edited ? [{ reset: true }, payload] : [{ reset: true }];
}

export async function requestSettings(scope = "global", method = "GET", payload, fetcher = fetch) {
  const response = await fetcher(settingsEndpoint(scope, method), {
    method,
    headers: payload ? { "Content-Type": "application/json", "X-Requested-With": "fetch" } : {},
    ...(payload ? { body: JSON.stringify(payload) } : {}),
  });
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new Error(text.length <= 200 ? text : "Could not update settings.");
  }
  return response.json();
}

export function configuredNodeVisibility(data) {
  return Object.fromEntries(allNodes.map(node => [node, data?.[`${node}_enabled`] === true]));
}

export function portControlModel(data) {
  const portIDs = allPortIDs;
  const ports = {};
  for (const port of Array.isArray(data?.ports) ? data.ports : []) {
    if (!portIDs.includes(port?.id)) continue;
    ports[port.id] = {
      id: port.id,
      enabled: port.enabled === true,
      bind_ip: typeof port.bind_ip === "string" ? port.bind_ip : "",
      host_port: Number.isInteger(port.host_port) && port.host_port >= 1 && port.host_port <= 65535 ? port.host_port : 0,
    };
  }
  const state = ["pending", "applied", "rejected", "failed"].includes(data?.status?.state) ? data.status.state : "";
  const affectedServices = Array.isArray(data?.status?.affected_services)
    ? data.status.affected_services.filter((service) => ["tor", "dashboard"].includes(service)) : [];
  const errorCode = data?.status?.error_code === "port_in_use" ? "port_in_use" : "";
  const errorPort = errorCode && portIDs.includes(data?.status?.port) ? data.status.port : "";
  return {
    available: data?.available === true,
    ports,
    state,
    message: state ? String(data.status.message || "") : "",
    affected_services: [...new Set(affectedServices)],
    error_code: state ? errorCode : "",
    port: state ? errorPort : "",
  };
}

export function portChanges(ports, draft) {
  const changes = [];
  for (const id of allPortIDs) {
    const before = ports?.[id];
    const after = draft?.[id];
    if (!before || !after) continue;
    const enabled = after.enabled === true;
    const hostPort = Number(after.host_port);
    if (enabled === before.enabled && (!enabled || hostPort === before.host_port)) continue;
    const change = { port: id, enabled };
    if (enabled) change.host_port = hostPort;
    changes.push(change);
  }
  return changes;
}

export function portCollision(changes, draft, ports) {
  for (const change of changes || []) {
    if (!change.enabled) continue;
    const bindIP = ports?.[change.port]?.bind_ip;
    const conflict = Object.entries(draft || {}).some(([id, port]) => id !== change.port
      && port?.enabled === true && Number(port.host_port) === change.host_port && ports?.[id]?.bind_ip === bindIP);
    if (conflict) return change.port;
  }
  return "";
}

export function portStatusMessage(view) {
  const labels = { bitcoin_rpc: "Bitcoin RPC", monero_rpc: "Monero RPC", monero_zmq: "Monero ZMQ", tari_grpc: "Tari gRPC", litecoin_rpc: "Litecoin RPC", dogecoin_rpc: "Dogecoin RPC", bitcoincash_rpc: "Bitcoin Cash RPC", zcash_rpc: "Zcash RPC" };
  if (view?.error_code === "port_in_use" && labels[view.port]) return `Selected port for ${labels[view.port]} is already in use.`;
  return String(view?.message || "");
}

export function portDraftAfterStatus(draft, state, requestMatches) {
  return requestMatches && state === "applied" ? undefined : draft;
}

export function portSubmissionOutcome(view, changes) {
  if (view?.state === "pending") return "queued";
  if (!Array.isArray(changes) || changes.length === 0) return "unknown";
  const applied = changes.every((change) => {
    const port = view?.ports?.[change.port];
    if (!port || port.enabled !== change.enabled) return false;
    return !change.enabled || port.host_port === change.host_port;
  });
  return applied ? "applied" : "unknown";
}

export function shouldPollPort(dialogOpen, state) {
  return dialogOpen === true && state === "pending";
}

export const TELEGRAM_OFF_NOTE = "Telegram is off, so no alerts are sent.";

export function settingsSaveMessage(settingsSaved, alertsOff = false) {
  return settingsSaved ? (alertsOff ? `Settings saved. ${TELEGRAM_OFF_NOTE}` : "Settings saved.") : "";
}

const defaultCurrencies = ["USD", "EUR", "GBP", "INR", "CAD", "AUD", "JPY", "RUB", "CNY"];

export function normalizeDefaultCurrency(value) {
  const currency = String(value || "").toUpperCase();
  return defaultCurrencies.includes(currency) ? currency : "USD";
}

export function applyAppearance(data, root = typeof document === "undefined" ? undefined : document.body, target = typeof document === "undefined" ? undefined : document) {
  const detail = { tower_labels: data?.tower_labels === true, computer_stats: data?.computer_stats === true, default_currency: normalizeDefaultCurrency(data?.default_currency) };
  if (root?.dataset) {
    root.dataset.towerLabels = detail.tower_labels ? "1" : "0";
    root.dataset.computerStats = detail.computer_stats ? "1" : "0";
  }
  if (target?.dispatchEvent) target.dispatchEvent(new CustomEvent("appearance-updated", { detail }));
  return detail;
}

export function splitNodeControlSections(alerts, target = document) {
  alerts.querySelector("summary").textContent = "Alerts";
  const lan = target.createElement("details");
  lan.className = "node-lan-settings";
  const summary = target.createElement("summary");
  summary.textContent = "LAN access";
  lan.append(summary, ...alerts.querySelectorAll(".settings-port"));
  return lan;
}

if (typeof document !== "undefined") {
  const byId = (id) => document.getElementById(`settings-${id}`);
  const dialog = byId("dialog");
  const form = byId("form");
  const status = byId("status");
  const token = byId("telegram-token");
  const chat = byId("telegram-chat");
  const url = byId("healthchecks-url");
  const telegramEnabled = byId("telegram-enabled");
  const alertPreferences = {
    alert_monitor_online: byId("alert-monitor-online"),
    alert_node_down: byId("alert-node-down"),
    alert_node_recovered: byId("alert-node-recovered"),
    alert_sync_complete: byId("alert-sync-complete"),
    alert_low_disk: byId("alert-low-disk"),
    alert_updates: byId("alert-updates"),
  };
  const nodeDownAfterMinutes = byId("node-down-after-minutes");
  const lowDiskGiB = byId("low-disk-gib");
  const healthchecksEnabled = byId("healthchecks-enabled");
  const bitcoinNewBlock = byId("bitcoin-new-block");
  const moneroNewBlock = byId("monero-new-block");
  const extraNewBlock = Object.fromEntries(extraNodes.map(node => [node, byId(`${node}-new-block`)]));
  const towerLabels = byId("tower-labels");
  const computerStats = byId("computer-stats");
  const thermalThrottle = byId("thermal-throttle");
  const defaultCurrency = byId("default-currency");
  const actuatorHealth = byId("actuator-health");
  const actuatorInfo = byId("actuator-info");
  const save = byId("save");
  const reset = byId("reset");
  const hostNodes = byId("host-nodes");
  const launchAtStart = byId("launch-at-start");
  const launchAtStartLabel = byId("launch-at-start-label");
  const configChange = byId("config-change");
  const applyConfig = byId("apply-config");
  const hostStatus = byId("host-status");
  byId("host").insertAdjacentHTML("afterend", `<fieldset id="settings-network" hidden>
    <legend>Network</legend>
    <label class="settings-check"><input id="settings-clearnet-over-tor" type="checkbox"> Reach clearnet peers through Tor</label>
    <label class="settings-check"><input id="settings-inbound-connections" type="checkbox"> Accept incoming connections over Tor</label>
    <p id="settings-network-status" class="settings-note" role="status" aria-live="polite"></p>
  </fieldset>`);
  byId("network").insertAdjacentHTML("afterend", `<fieldset id="settings-balance-index" hidden>
    <legend>Balance index</legend>
    <label class="settings-check"><input id="settings-utxo-index" type="checkbox" role="switch"> Instant Bitcoin balances</label>
    <label id="settings-utxo-index-litecoin-label" class="settings-check" hidden><input id="settings-utxo-index-litecoin" type="checkbox" role="switch"> Instant Litecoin balances</label>
    <label id="settings-utxo-index-bitcoincash-label" class="settings-check" hidden><input id="settings-utxo-index-bitcoincash" type="checkbox" role="switch"> Instant Bitcoin Cash balances</label>
    <label id="settings-utxo-index-dogecoin-label" class="settings-check" hidden><input id="settings-utxo-index-dogecoin" type="checkbox" role="switch"> Instant Dogecoin balances</label>
    <p id="settings-utxo-index-status" class="settings-note" role="status" aria-live="polite"></p>
  </fieldset>`);
  const utxoIndex = byId("utxo-index");
  const balanceSettings = byId("balance-index");
  const balanceStatus = byId("utxo-index-status");
  const utxoIndexLitecoin = byId("utxo-index-litecoin");
  const utxoIndexLitecoinLabel = byId("utxo-index-litecoin-label");
  const utxoIndexBitcoinCash = byId("utxo-index-bitcoincash");
  const utxoIndexBitcoinCashLabel = byId("utxo-index-bitcoincash-label");
  const utxoIndexDogecoin = byId("utxo-index-dogecoin");
  const utxoIndexDogecoinLabel = byId("utxo-index-dogecoin-label");
  const clearnetOverTor = byId("clearnet-over-tor");
  const inboundConnections = byId("inbound-connections");
  const networkStatus = byId("network-status");
  const networkSettings = byId("network");
  const hostStatusTarget = (action) => balanceActions.includes(action) ? balanceStatus : networkActions.includes(action) ? networkStatus : hostStatus;
  const configPath = byId("config-path");
  const deleteDialog = byId("delete-dialog");
  const deleteForm = byId("delete-form");
  const deleteTitle = byId("delete-title");
  const deleteList = byId("delete-list");
  const deleteConfirmLabel = byId("delete-confirm-label");
  const deleteConfirm = byId("delete-confirm");
  const deleteSubmit = byId("delete-submit");
  let deleteNode = "";
  const installDialog = byId("install-dialog");
  const installForm = byId("install-form");
  const installTitle = byId("install-title");
  const installDisk = byId("install-disk");
  const installDataDir = byId("install-data-dir");
  const installInterfaces = byId("install-interfaces");
  const installIndexLabel = byId("install-index-label");
  const installIndex = byId("install-index");
  const installIndexHint = byId("install-index-hint");
  const installError = byId("install-error");
  const installSubmit = byId("install-submit");
  let installAction = "install";
  const nodeViews = {};
  for (const node of allNodes) {
    const details = byId(`${node}-alerts`);
    const panel = document.querySelector(`[data-node-panel="${node}"]`);
    const power = panel?.querySelector(".node-power-card");
    if (!details || !power) continue;
    byId(`${node}-new-block`).closest("label").insertAdjacentHTML("afterend", `<p class="settings-note" data-telegram-off-note hidden>${TELEGRAM_OFF_NOTE}</p>`);
    const lan = splitNodeControlSections(details);
    const nodeForm = document.createElement("form");
    nodeForm.className = "node-settings-form";
    nodeForm.dataset.nodeSettingsForm = node;
    const nodeStatus = document.createElement("p");
    nodeStatus.className = "settings-note";
    nodeStatus.setAttribute("role", "status");
    nodeStatus.setAttribute("aria-live", "polite");
    const impact = document.createElement("p");
    impact.className = "settings-note";
    impact.textContent = "Will restart services: Tor relay, dashboard.";
    impact.hidden = true;
    const actions = document.createElement("div");
    actions.className = "settings-actions";
    const nodeSave = document.createElement("button");
    nodeSave.type = "submit";
    nodeSave.className = "primary";
    nodeSave.textContent = "Save";
    actions.append(nodeSave);
    nodeForm.append(details, lan, nodeStatus, impact, actions);
    power.querySelector("h2").after(nodeForm);
    nodeViews[node] = { form: nodeForm, status: nodeStatus, impact, save: nodeSave };
  }
  byId("port-status")?.remove();
  byId("port-impact")?.remove();
  const accountDialog = document.getElementById("account-dialog");
  const credentialSettings = credentialSettingsController(accountDialog, document);
  const actuatorCurl = actuatorCurlController(document);
  let saved = {};
  let savedPorts = {};
  let allowed = false;
  let portAvailable = false;
  let portState = "";
  let portPoll;
  let portRequestID = "";
  let ambiguousPortDraft;
  let ambiguousStatusPrefix = "";
  let ambiguousStatusTarget;
  let ambiguousAlerts;
  let submittedPortNode = "";
  let stagedPortDraft;
  let hostPoll;
  let hostView = hostControlModel();
  let hostLoaded = false;
  let pendingToggle;
  let hostDraft = {};
  let hostStaged = {};
  let resetStaged = false;
  let hostQueue = [];
  let hostQueueAction = "";
  let hostQueueChange = null;
  let hostFailures = [];
  let hostSkipped = [];
  const saving = () => hostQueue.length > 0 || hostQueueAction !== "";
  const hostToggles = { launch_at_boot: launchAtStart, clearnet_over_tor: clearnetOverTor, inbound_connections: inboundConnections, utxo_index: utxoIndex, utxo_index_litecoin: utxoIndexLitecoin, utxo_index_bitcoincash: utxoIndexBitcoinCash, utxo_index_dogecoin: utxoIndexDogecoin };
  const hostRequests = latestRequestGate();
  let installNode = "";
  try { portRequestID = localStorage.getItem("nodeStackPortRequest") || ""; } catch {}

  const portRows = {
    bitcoin_rpc: byId("port-bitcoin-rpc"),
    monero_rpc: byId("port-monero-rpc"),
    monero_zmq: byId("port-monero-zmq"),
    ...Object.fromEntries(extraNodes.map(node => [`${node}_${node === "tari" ? "grpc" : "rpc"}`, byId(`port-${node}-${node === "tari" ? "grpc" : "rpc"}`)])),
  };

  const portDraft = () => Object.fromEntries(Object.entries(portRows).map(([id, row]) => [id, {
    enabled: row.querySelector("[data-port-enabled]").checked,
    host_port: Number(row.querySelector("[data-port-number]").value),
  }]));
  const changesForNode = (node, changes = portChanges(savedPorts, portDraft())) => changes.filter((change) => nodePortIDs[node].includes(change.port));

  const applyPortDraft = (draft, remember = true) => {
    for (const [id, values] of Object.entries(draft || {})) {
      const row = portRows[id];
      if (!row) continue;
      row.querySelector("[data-port-enabled]").checked = values.enabled === true;
      row.querySelector("[data-port-number]").value = values.host_port || "";
    }
    if (remember) stagedPortDraft = draft;
    updatePortPlan(false);
  };

  const updatePortPlan = (remember = false) => {
    const pending = portState === "pending";
    const draft = portDraft();
    const changes = portChanges(savedPorts, draft);
    if (remember) stagedPortDraft = changes.length ? draft : undefined;
    for (const row of Object.values(portRows)) {
      const enabled = row.querySelector("[data-port-enabled]");
      const locked = !allowed || !portAvailable || pending || row.hidden;
      enabled.disabled = locked;
      row.querySelector("[data-port-number]").disabled = locked || !enabled.checked;
    }
    for (const [node, view] of Object.entries(nodeViews)) {
      const nodeChanges = changesForNode(node, changes);
      view.impact.hidden = nodeChanges.length === 0;
      view.save.textContent = nodeChanges.length > 0 ? (pending ? "Restart pending" : "Save & restart") : "Save";
      view.save.disabled = !allowed || pending;
    }
    save.textContent = "Save";
    save.disabled = !allowed || pending;
    reset.disabled = !allowed || pending || !saved.defaults;
    return changes;
  };

  const renderPortControl = (data) => {
    const view = portControlModel(data);
    const statusID = String(data?.status?.id || "");
    const requestMatches = !!portRequestID && statusID === portRequestID;
    if (view.state === "pending" && /^[0-9a-f]{32}$/.test(String(data?.status?.id || ""))) {
      portRequestID = data.status.id;
      try { localStorage.setItem("nodeStackPortRequest", portRequestID); } catch {}
    } else if (view.state && data?.status?.id !== portRequestID) {
      view.state = "";
      view.message = "";
      view.error_code = "";
      view.port = "";
    } else if (view.state && view.state !== "pending") {
      try { localStorage.removeItem("nodeStackPortRequest"); } catch {}
      portRequestID = "";
    }
    stagedPortDraft = portDraftAfterStatus(stagedPortDraft, view.state, requestMatches);
    savedPorts = view.ports;
    portAvailable = view.available;
    portState = view.state;
    for (const [id, row] of Object.entries(portRows)) {
      const port = view.ports[id];
      row.hidden = !port;
      if (!port) continue;
      row.querySelector("[data-port-enabled]").checked = port.enabled;
      row.querySelector("[data-port-number]").value = port.host_port || "";
    }
    if (stagedPortDraft) applyPortDraft(stagedPortDraft, false);
    const message = portStatusMessage(view);
    const changedNodes = Object.keys(nodeViews).filter((node) => changesForNode(node).length);
    const messageNode = portStatusNode(view.port, submittedPortNode, changedNodes);
    for (const [node, nodeView] of Object.entries(nodeViews)) {
      nodeView.status.textContent = message && node === messageNode
        ? message
        : !view.available && nodePortIDs[node].some((id) => view.ports[id])
          ? "LAN port changes require a dashboard password and the installed host helper."
          : "";
    }
    clearTimeout(portPoll);
    if (shouldPollPort(dialog.open || !!document.body.dataset.node, view.state)) portPoll = setTimeout(loadPortControl, 2000);
    updatePortPlan();
    if (requestMatches && view.state === "applied" && document.body.dataset.node) credentialSettings.openNode();
    if (view.state && view.state !== "pending") submittedPortNode = "";
    return view;
  };

  const clearSecrets = () => { token.value = ""; chat.value = ""; url.value = ""; };

  const requestHost = async (payload) => {
    const response = await fetch("/api/settings/host", {
      method: payload ? "POST" : "GET",
      headers: payload ? { "Content-Type": "application/json", "X-Requested-With": "fetch" } : {},
      ...(payload ? { body: JSON.stringify(payload) } : {}),
    });
    if (!response.ok) {
      const message = (await response.text()).trim();
      throw new Error(message.length <= 200 ? message : "Could not update the node set.");
    }
    return response.json();
  };

  const renderHost = (data) => {
    const nextHostView = hostControlModel(data);
    if (pendingToggle) {
      const field = toggleFields[pendingToggle.action];
      const failed = toggleKey(nextHostView.status) === pendingToggle.action && ["rejected", "failed"].includes(nextHostView.status.state);
      if (failed || nextHostView[field] === pendingToggle.enabled) pendingToggle = undefined;
      else {
        nextHostView[field] = pendingToggle.enabled;
        const node = chainIndexNode(pendingToggle.action);
        nextHostView.status = { action: node ? "utxo_index" : pendingToggle.action, node, state: "pending", message: hostView.status.message || "Applying host change…" };
      }
    }
    const queueResult = hostQueueSettled(nextHostView, hostQueueChange);
    if (queueResult === "failed") {
      hostFailures.push({ label: hostChangeLabel(hostQueueChange, nextHostView.nodes), message: nextHostView.status.message || "The host change failed." });
      const { rest, skipped } = splitDependentChanges(hostQueue, hostQueueChange);
      hostSkipped.push(...skipped.map((change) => hostChangeLabel(change, nextHostView.nodes)));
      hostQueue = rest;
    }
    if (queueResult && !hostQueue.length) {
      status.textContent = hostQueueSummary(hostFailures, hostSkipped);
      hostQueue = [];
      hostDraft = {};
      hostStaged = {};
      hostFailures = [];
      hostSkipped = [];
    }
    if (queueResult) {
      hostQueueAction = "";
      hostQueueChange = null;
    }
    const installed = hostLoaded ? newlyInstalledNodes(hostView, nextHostView) : [];
    hostView = nextHostView;
    const shown = (action) => Object.hasOwn(hostDraft, action) ? hostDraft[action] : hostView[toggleFields[action]];
    hostLoaded = true;
    const busy = hostView.status.state === "pending";
    renderHostActions();
    launchAtStart.checked = shown("launch_at_boot");
    launchAtStart.disabled = !hostView.available || !hostView.boot_supported || hostView.config_pending || busy;
    launchAtStartLabel.querySelector("span").textContent = bootControlLabel(hostView.boot_kind);
    launchAtStartLabel.hidden = hostView.boot_kind === "unsupported";
    networkSettings.hidden = !hostView.available;
    clearnetOverTor.checked = shown("clearnet_over_tor");
    inboundConnections.checked = shown("inbound_connections");
    clearnetOverTor.disabled = inboundConnections.disabled = !hostView.available || hostView.config_pending || busy;
    const balanceControl = balanceIndexControl({ ...hostView, utxo_index: shown("utxo_index") }, busy);
    balanceSettings.hidden = balanceControl.hidden;
    utxoIndex.checked = balanceControl.checked;
    utxoIndex.disabled = balanceControl.disabled;
    utxoIndex.closest("label").hidden = balanceControl.hidden;
    utxoIndexLitecoinLabel.hidden = !hostView.available || !hostView.nodes.litecoin.installed;
    utxoIndexLitecoin.checked = shown("utxo_index_litecoin");
    utxoIndexLitecoin.disabled = balanceControl.disabled;
    utxoIndexBitcoinCashLabel.hidden = !hostView.available || !hostView.nodes.bitcoincash.installed;
    utxoIndexBitcoinCash.checked = shown("utxo_index_bitcoincash");
    utxoIndexBitcoinCash.disabled = balanceControl.disabled;
    utxoIndexDogecoinLabel.hidden = !hostView.available || !hostView.nodes.dogecoin.installed;
    utxoIndexDogecoin.checked = shown("utxo_index_dogecoin");
    utxoIndexDogecoin.disabled = balanceControl.disabled;
    for (const [node, input] of [["bitcoin", utxoIndex], ["litecoin", utxoIndexLitecoin], ["bitcoincash", utxoIndexBitcoinCash], ["dogecoin", utxoIndexDogecoin]]) {
      const refusal = hostView.nodes[node].index_refusal;
      if (refusal) input.disabled = true;
      input.title = refusal;
    }
    balanceSettings.hidden = balanceControl.hidden && utxoIndexLitecoinLabel.hidden && utxoIndexBitcoinCashLabel.hidden && utxoIndexDogecoinLabel.hidden;
    configChange.hidden = !hostView.config_pending;
    const networkMessage = networkActions.includes(hostView.status.action);
    const balanceMessage = balanceActions.includes(hostView.status.action);
    networkStatus.textContent = networkMessage ? hostView.status.message : "";
    balanceStatus.textContent = balanceMessage ? hostView.status.message : "";
    hostStatus.textContent = networkMessage || balanceMessage ? "" : hostView.status.message || (!hostView.available ? "Host controls require the installed helper." : "");
    configPath.textContent = hostView.config_path || "Unavailable until the installed helper reports it.";
    clearTimeout(hostPoll);
    if (shouldPollHost(hostView)) hostPoll = setTimeout(loadHost, 2000);
    if (installed.length) window.dispatchEvent(new CustomEvent("node-catalog-updated", { detail: { installed } }));
    if (!busy && hostQueue.length) submitNextHostChange();
  };

  const renderHostActions = () => {
    hostNodes.replaceChildren();
    const busy = hostView.status.state === "pending";
    const installing = pendingHostNode(hostView, "install");
    const moving = pendingHostNode(hostView, "migrate");
    const deleting = pendingHostNode(hostView, "delete");
    for (const node of Object.values(hostView.nodes)) {
      const row = document.createElement("div");
      row.className = "settings-host-node";
      const name = document.createElement("span");
      name.textContent = node.name;
      const action = node.installed ? "migrate" : "install";
      const key = `${action}:${node.id}`;
      const button = document.createElement("button");
      button.type = "button";
      if (hostStaged[key]) {
        button.textContent = node.installed ? "Moves on Save" : "Installs on Save";
        button.title = "Click to undo";
        button.disabled = busy;
        button.addEventListener("click", () => { delete hostStaged[key]; renderHostActions(); });
      } else if (node.installed) {
        button.textContent = node.id === moving ? "Moving data…" : "Move data…";
        button.disabled = !node.data_dir || !hostView.available || !hostView.migrate_supported || hostView.config_pending || busy;
        button.addEventListener("click", () => openMigrate(node.id));
      } else {
        button.textContent = node.id === installing ? "Installing…" : "Install";
        button.disabled = !node.data_dir || !hostView.available || !hostView.install_supported || hostView.config_pending || busy || !!node.unsupported;
        if (node.unsupported) button.title = node.unsupported;
        button.addEventListener("click", () => openInstall(node.id));
      }
      if (node.installed) {
        const path = document.createElement("code");
        path.className = "settings-host-path";
        path.textContent = node.data_dir || "";
        path.title = "Current data folder";
        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "settings-delete-button";
        const deleteKey = `delete:${node.id}`;
        if (hostStaged[deleteKey]) {
          remove.textContent = "Deletes on Save";
          remove.title = "Click to undo";
          remove.disabled = busy;
          remove.addEventListener("click", () => { delete hostStaged[deleteKey]; renderHostActions(); });
          button.disabled = true;
        } else {
          const control = deleteControl(hostView, node, hostStaged);
          remove.textContent = node.id === deleting ? "Deleting…" : "Delete…";
          remove.disabled = control.disabled;
          remove.title = control.reason || `Delete ${node.name} and all its data`;
          remove.addEventListener("click", () => openDelete(node.id));
        }
        row.append(name, path, button, remove);
      } else if (node.unsupported) {
        const note = document.createElement("span");
        note.className = "settings-host-note";
        note.textContent = node.unsupported;
        row.append(name, note, button);
      } else row.append(name, button);
      hostNodes.append(row);
    }
    applyConfig.textContent = hostStaged.apply_config ? "Runs on Save" : "Run new config";
    applyConfig.disabled = !hostView.available || busy;
  };

  const submitNextHostChange = () => {
    const next = hostQueue.shift();
    hostQueueAction = next.action;
    hostQueueChange = { ...next };
    submitHost(next);
  };

  const openDelete = (nodeID) => {
    const node = hostView.nodes[nodeID];
    if (!node || deleteControl(hostView, node, hostStaged).disabled) return;
    deleteNode = nodeID;
    deleteTitle.textContent = `Delete ${node.name}?`;
    deleteList.replaceChildren(...deleteImpact(node).map((text) => {
      const item = document.createElement("li");
      item.textContent = text;
      return item;
    }));
    deleteConfirmLabel.querySelector("b").textContent = node.id;
    deleteConfirm.value = "";
    deleteSubmit.disabled = true;
    deleteDialog.showModal();
    deleteConfirm.focus();
  };

  const openMigrate = (nodeID) => {
    const node = hostView.nodes[nodeID];
    if (!node || !node.installed || !node.data_dir) return;
    installNode = nodeID;
    installAction = "migrate";
    installTitle.textContent = `Move ${node.name} data`;
    installDisk.textContent = `Currently at ${node.data_dir}. The node stops while its data moves, then restarts from the new folder; moving to another disk copies everything first and can take a long time.`;
    installDataDir.value = node.data_dir;
    installInterfaces.replaceChildren();
    installIndexLabel.hidden = installIndexHint.hidden = true;
    installError.textContent = "";
    installSubmit.textContent = "Move on Save";
    installDialog.showModal();
  };

  const openInstall = (nodeID) => {
    const node = hostView.nodes[nodeID];
    if (!node || node.installed || !node.data_dir) return;
    installNode = nodeID;
    installAction = "install";
    installTitle.textContent = `Install ${node.name}`;
    installDisk.textContent = `Estimated disk after sync: ${node.disk_estimate}. ${node.disk_note}`;
    installDataDir.value = node.data_dir;
    installInterfaces.replaceChildren();
    for (const item of node.interfaces) {
      const row = document.createElement("div");
      row.className = "settings-install-interface";
      row.dataset.interface = item.id;
      const toggle = document.createElement("label");
      toggle.className = "settings-check";
      const checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      checkbox.dataset.interfaceEnabled = "";
      toggle.append(checkbox, ` Open ${item.name} to the private LAN`);
      const portLabel = document.createElement("label");
      portLabel.textContent = "Port";
      const port = document.createElement("input");
      port.type = "number";
      port.min = "1";
      port.max = "65535";
      port.step = "1";
      port.value = String(item.host_port);
      port.dataset.interfacePort = "";
      port.disabled = true;
      checkbox.addEventListener("change", () => { port.disabled = !checkbox.checked; });
      portLabel.append(port);
      row.append(toggle, portLabel);
      installInterfaces.append(row);
    }
    const offered = node.can_index && hostView.install_index_supported;
    installIndexLabel.hidden = installIndexHint.hidden = !offered;
    installIndex.checked = true;
    installError.textContent = "";
    installSubmit.textContent = "Install on Save";
    installDialog.showModal();
  };

  async function loadHost() {
    await hostRequests.run(requestHost, renderHost, (error) => {
      hostStatus.textContent = error.message;
      clearTimeout(hostPoll);
      if (shouldPollHost(hostView)) hostPoll = setTimeout(loadHost, 2000);
    });
  }

  async function submitHost(payload) {
    if (!payload) return;
    hostRequests.invalidate();
    clearTimeout(hostPoll);
    const target = hostStatusTarget(payload.action);
    target.textContent = "Applying…";
    for (const button of hostNodes.querySelectorAll("button")) button.disabled = true;
    launchAtStart.disabled = applyConfig.disabled = clearnetOverTor.disabled = inboundConnections.disabled = utxoIndex.disabled = utxoIndexLitecoin.disabled = utxoIndexBitcoinCash.disabled = utxoIndexDogecoin.disabled = true;
    if (toggleFields[toggleKey(payload)]) pendingToggle = { action: toggleKey(payload), enabled: payload.enabled };
    try {
      const statusView = await requestHost(payload);
      if (hostQueueChange && /^[0-9a-f]{32}$/.test(String(statusView.id || ""))) hostQueueChange.requestId = statusView.id;
      hostView.status = { action: payload.action, node: payload.node || "", state: "pending", message: statusView.message || "Applying host change…" };
      target.textContent = hostView.status.message;
      await loadHost();
    } catch (error) {
      pendingToggle = undefined;
      hostFailures.push({ label: hostChangeLabel(payload, hostView.nodes), message: error.message });
      const { rest, skipped } = splitDependentChanges(hostQueue, payload);
      hostSkipped.push(...skipped.map((change) => hostChangeLabel(change, hostView.nodes)));
      hostQueue = rest;
      hostQueueAction = "";
      hostQueueChange = null;
      if (hostQueue.length) {
        submitNextHostChange();
        return;
      }
      target.textContent = hostQueueSummary(hostFailures, hostSkipped);
      hostDraft = {};
      hostStaged = {};
      hostFailures = [];
      hostSkipped = [];
      await loadHost();
    }
  }
  const updateInputs = () => {
    token.disabled = chat.disabled = !allowed || !telegramEnabled.checked;
    for (const input of Object.values(alertPreferences)) input.disabled = !allowed || !telegramEnabled.checked;
    nodeDownAfterMinutes.disabled = lowDiskGiB.disabled = !allowed || !telegramEnabled.checked;
    url.disabled = !allowed || !healthchecksEnabled.checked;
    chat.required = allowed && telegramEnabled.checked;
    token.required = chat.required && !saved.telegram_bot_token;
    url.required = allowed && healthchecksEnabled.checked && !saved.healthchecks_url;
  };
  const renderGlobalFields = (data) => {
    telegramEnabled.checked = !!data.telegram_enabled;
    healthchecksEnabled.checked = !!data.healthchecks_enabled;
    const preferences = alertPreferenceModel(data);
    for (const [field, input] of Object.entries(alertPreferences)) input.checked = preferences[field];
    nodeDownAfterMinutes.value = preferences.node_down_after_minutes;
    lowDiskGiB.value = preferences.low_disk_gib;
    towerLabels.checked = !!data.tower_labels;
    computerStats.checked = !!data.computer_stats;
    thermalThrottle.checked = !!data.thermal_throttle;
    defaultCurrency.value = normalizeDefaultCurrency(data.default_currency);
    actuatorHealth.checked = !!data.actuator_health;
    actuatorInfo.checked = !!data.actuator_info;
  };
  const render = (data, scope = "global") => {
    saved = { ...saved, ...data };
    allowed = configurationAccess(data);
    if (Object.hasOwn(data, "dashboard_password_set")) credentialSettings.setPasswordConfigured(data.dashboard_password_set === true);
    if (scope === "global") renderGlobalFields(data);
    if (Object.hasOwn(data, "alert_new_block_bitcoin") || Object.hasOwn(data, "alert_new_block")) bitcoinNewBlock.checked = !!(data.alert_new_block_bitcoin ?? data.alert_new_block);
    if (Object.hasOwn(data, "alert_new_block_monero")) moneroNewBlock.checked = !!data.alert_new_block_monero;
    const nodes = configuredNodeVisibility(data);
    for (const node of allNodes) {
      byId(`${node}-alerts`).hidden = !nodes[node];
      if (nodeViews[node]) nodeViews[node].form.hidden = !nodes[node];
    }
    for (const [node,input] of Object.entries(extraNewBlock)) {
      if (Object.hasOwn(data, `alert_new_block_${node}`)) input.checked = !!data[`alert_new_block_${node}`];
      input.disabled = !allowed;
    }
    renderPortControl(data.port_control);
    actuatorCurl.render(saved, allowed);
    if (scope === "global") {
      applyAppearance(data);
      if (Object.hasOwn(data, "telegram_chat_id")) chat.value = data.telegram_chat_id || "";
      if (dialog.open) {
        if (Object.hasOwn(data, "telegram_bot_token")) token.value = data.telegram_bot_token || "";
        if (Object.hasOwn(data, "healthchecks_url")) url.value = data.healthchecks_url || "";
      } else clearSecrets();
    }
    token.placeholder = "Paste your bot token";
    url.placeholder = "https://hc-ping.com/…";
    reset.disabled = !allowed || !saved.defaults;
    for (const note of document.querySelectorAll("[data-telegram-off-note]")) note.hidden = !!saved.telegram_enabled;
    telegramEnabled.disabled = healthchecksEnabled.disabled = bitcoinNewBlock.disabled = moneroNewBlock.disabled = towerLabels.disabled = computerStats.disabled = thermalThrottle.disabled = defaultCurrency.disabled = actuatorHealth.disabled = actuatorInfo.disabled = !allowed;
    updateInputs();
    updatePortPlan();
  };
  async function request(method = "GET", payload, scope = "global") {
    return requestSettings(scope, method, payload);
  }
  async function requestPortControl(method = "GET", payload) {
    const response = await fetch("/api/settings/ports", {
      method,
      headers: payload ? { "Content-Type": "application/json", "X-Requested-With": "fetch" } : {},
      ...(payload ? { body: JSON.stringify(payload) } : {}),
    });
    if (!response.ok) {
      const text = (await response.text()).trim();
      const error = new Error(text.length <= 200 ? text : "Could not update LAN ports.");
      error.definitive = true;
      throw error;
    }
    return response.json();
  }
  const ambiguousMessage = () => settingsSaveMessage(ambiguousStatusPrefix !== "", ambiguousAlerts ? nodeAlertsOff(ambiguousAlerts.scope, ambiguousAlerts.payload, saved) : false);
  async function loadPortControl() {
    try {
      const data = await requestPortControl();
      const view = portControlModel(data);
      renderPortControl(data);
      if (ambiguousPortDraft) {
        const { draft, changes } = ambiguousPortDraft;
        const outcome = portSubmissionOutcome(view, changes);
        if (outcome === "queued") {
          ambiguousPortDraft = undefined;
          (ambiguousStatusTarget || status).textContent = ambiguousMessage();
        } else if (outcome === "applied") {
          ambiguousPortDraft = undefined;
          stagedPortDraft = undefined;
          updatePortPlan(false);
          (ambiguousStatusTarget || status).textContent = ambiguousMessage();
        } else {
          ambiguousPortDraft = undefined;
          applyPortDraft(draft);
          (ambiguousStatusTarget || status).textContent = `${ambiguousStatusPrefix}The restart request status is unknown; review the LAN port values before trying again.`;
        }
        ambiguousStatusPrefix = "";
        ambiguousStatusTarget = undefined;
        ambiguousAlerts = undefined;
      }
    }
    catch { if (dialog.open || document.body.dataset.node) portPoll = setTimeout(loadPortControl, 2000); }
  }
  async function load(scope = "global") {
    allowed = false;
    save.disabled = reset.disabled = true;
    telegramEnabled.disabled = healthchecksEnabled.disabled = bitcoinNewBlock.disabled = moneroNewBlock.disabled = towerLabels.disabled = computerStats.disabled = thermalThrottle.disabled = defaultCurrency.disabled = actuatorHealth.disabled = actuatorInfo.disabled = true;
    for (const input of Object.values(alertPreferences)) input.disabled = true;
    nodeDownAfterMinutes.disabled = lowDiskGiB.disabled = true;
    updateInputs();
    updatePortPlan();
    for (const input of Object.values(extraNewBlock)) input.disabled = true;
    status.textContent = "Loading…";
    try {
      render(await request("GET", undefined, scope), scope);
      status.textContent = allowed ? "" : "Settings are unavailable.";
    }
    catch (error) { status.textContent = error.message; }
  }
  byId("open").addEventListener("click", () => {
    if (!hostQueueAction) {
      hostDraft = {};
      hostStaged = {};
      for (const [action, input] of Object.entries(hostToggles)) input.checked = hostView[toggleFields[action]];
      renderHostActions();
    }
    resetStaged = false;
    dialog.showModal();
    load().then(() => { if (discardedNotice) status.textContent = "Settings closed before you confirmed, so the staged changes were discarded."; discardedNotice = false; });
    loadHost();
  });
  let closeAsking = false;
  let discardedNotice = false;
  const closeSettings = async () => {
    if (closeAsking) return;
    closeAsking = true;
    const discard = saving() || !(Object.keys(hostStaged).length || resetStaged) || await askConfirm("Discard the staged changes?", "Discard");
    closeAsking = false;
    if (!discard) return;
    dialog.close();
  };
  for (const id of ["close", "cancel"]) byId(id).addEventListener("click", closeSettings);
  dialog.addEventListener("cancel", (event) => { if (!event.cancelable) { discardedNotice = !saving() && (Object.keys(hostStaged).length > 0 || resetStaged); return; } event.preventDefault(); closeSettings(); });
  document.getElementById("account-open").addEventListener("click", () => { accountDialog.showModal(); credentialSettings.openPassword(); });
  document.getElementById("account-close").addEventListener("click", () => accountDialog.close());
  accountDialog.addEventListener("close", () => credentialSettings.closePassword());
  byId("install-cancel").addEventListener("click", () => installDialog.close());
  byId("delete-cancel").addEventListener("click", () => deleteDialog.close());
  deleteConfirm.addEventListener("input", () => { deleteSubmit.disabled = !deleteConfirmed(hostView.nodes[deleteNode], deleteConfirm.value.trim()); });
  deleteForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const payload = hostActionPayload("delete", deleteNode, { confirmation: deleteConfirm.value.trim() });
    if (!payload) return;
    hostStaged[`delete:${deleteNode}`] = payload;
    deleteDialog.close();
    renderHostActions();
  });
  installForm.addEventListener("submit", (event) => {
    event.preventDefault();
    let payload;
    if (installAction === "migrate") {
      const target = installDataDir.value.trim();
      if (target === hostView.nodes[installNode]?.data_dir) {
        installError.textContent = "Choose a different folder than the current one.";
        return;
      }
      payload = hostActionPayload("migrate", installNode, { data_dir: target });
    } else {
      const interfaces = Array.from(installInterfaces.children).map((row) => ({
        id: row.dataset.interface,
        enabled: row.querySelector("[data-interface-enabled]").checked,
        host_port: Number(row.querySelector("[data-interface-port]").value),
      }));
      const offered = !installIndexLabel.hidden;
      payload = hostActionPayload("install", installNode, { data_dir: installDataDir.value.trim(), interfaces, ...(offered ? { utxo_index: installIndex.checked } : {}) });
    }
    if (!payload) {
      installError.textContent = "Choose an absolute host folder and valid ports from 1 to 65535.";
      return;
    }
    hostStaged[`${installAction}:${installNode}`] = payload;
    installDialog.close();
    renderHostActions();
  });
  dialog.addEventListener("close", () => { actuatorCurl.closeDialog(); clearSecrets(); delete saved.telegram_bot_token; delete saved.telegram_chat_id; delete saved.healthchecks_url; });
  window.addEventListener("node-selected", (event) => {
    credentialSettings.closeNode();
    if (event.detail?.node) {
      credentialSettings.openNode();
      load("nodes");
    }
  });
  telegramEnabled.addEventListener("change", updateInputs);
  healthchecksEnabled.addEventListener("change", updateInputs);
  for (const [action, input] of Object.entries(hostToggles)) input.addEventListener("change", () => { hostDraft[action] = input.checked; });
  applyConfig.addEventListener("click", () => {
    if (hostStaged.apply_config) delete hostStaged.apply_config;
    else hostStaged.apply_config = hostActionPayload("apply_config", hostView.config_revision);
    renderHostActions();
  });
  for (const row of Object.values(portRows)) {
    for (const input of row.querySelectorAll("input")) input.addEventListener("input", () => updatePortPlan(true));
  }

  const settingsPayload = (scope = "global") => scopedSettingsPayload(scope, scope === "global" ? {
      telegram_enabled: telegramEnabled.checked,
      telegram_bot_token: token.value,
      telegram_chat_id: chat.value,
      healthchecks_enabled: healthchecksEnabled.checked,
      healthchecks_url: url.value,
      tower_labels: towerLabels.checked,
      computer_stats: computerStats.checked,
      thermal_throttle: thermalThrottle.checked,
      default_currency: defaultCurrency.value,
      actuator_health: actuatorHealth.checked,
      actuator_info: actuatorInfo.checked,
      ...Object.fromEntries(Object.entries(alertPreferences).map(([field, input]) => [field, input.checked])),
      node_down_after_minutes: Number(nodeDownAfterMinutes.value),
      low_disk_gib: Number(lowDiskGiB.value),
    } : { [scope === "bitcoin" ? "alert_new_block" : `alert_new_block_${scope}`]:
      (scope === "bitcoin" ? bitcoinNewBlock : scope === "monero" ? moneroNewBlock : extraNewBlock[scope]).checked });

  const validatePortPlan = (changes) => {
    for (const row of Object.values(portRows)) row.querySelector("[data-port-number]").setCustomValidity("");
    for (const change of changes) {
      if (!change.enabled || Number.isInteger(change.host_port) && change.host_port >= 1 && change.host_port <= 65535) continue;
      portRows[change.port].querySelector("[data-port-number]").setCustomValidity("Enter a port from 1 to 65535.");
      return false;
    }
    const collision = portCollision(changes, portDraft(), savedPorts);
    if (collision) {
      portRows[collision].querySelector("[data-port-number]").setCustomValidity(portStatusMessage({ error_code: "port_in_use", port: collision }));
      return false;
    }
    return true;
  };

  const nodeAlertsOff = (scope, payload, view) => scope !== "global" && Object.values(payload).includes(true) && !view.telegram_enabled;
  const saveScope = async (scope, scopeForm, targetStatus, targetSave) => {
    if (!allowed) return;
    const allChanges = updatePortPlan(true);
    const changes = scope === "global" ? [] : changesForNode(scope, allChanges);
    const draft = portDraft();
    const portPlanValid = validatePortPlan(changes);
    const formValid = scopeForm.reportValidity();
    if (!portPlanValid || !formValid) return;
    const payload = settingsPayload(scope);
    const resetRequests = scope === "global" && resetStaged ? resetSaveRequests(saved.defaults, payload) : [];
    const saveNotifications = !resetRequests.length && notificationChanged(saved, payload);
    let hostChanges = scope === "global" && !hostQueueAction ? savedHostActions(hostView, hostDraft, hostStaged) : [];
    if (!resetRequests.length && !saveNotifications && changes.length === 0 && hostChanges.length === 0) {
      targetStatus.textContent = "No changes.";
      return;
    }
    if (hostChanges.length) {
      const confirmed = await confirmedHostChanges(hostChanges, (message) => askConfirm(message, "Turn off"));
      if (confirmed.length < hostChanges.length) {
        delete hostDraft.utxo_index;
        delete hostDraft.utxo_index_litecoin;
        delete hostDraft.utxo_index_bitcoincash;
        delete hostDraft.utxo_index_dogecoin;
        utxoIndex.checked = hostView.utxo_index;
        utxoIndexLitecoin.checked = hostView.utxo_index_litecoin;
        utxoIndexBitcoinCash.checked = hostView.utxo_index_bitcoincash;
        utxoIndexDogecoin.checked = hostView.utxo_index_dogecoin;
      }
      hostChanges = confirmed;
    }
    targetSave.disabled = true;
    if (scope === "global") reset.disabled = true;
    targetStatus.textContent = "Saving…";
    let settingsView = saved;
    if (resetRequests.length || saveNotifications) {
      try {
        for (const body of resetRequests.length ? resetRequests : [payload]) settingsView = await request("POST", body, scope);
        resetStaged = false;
      } catch (error) {
        clearSecrets();
        targetStatus.textContent = error.message;
        targetSave.disabled = !allowed;
        reset.disabled = !allowed || !saved.defaults;
        updateInputs();
        updatePortPlan();
        return;
      }
    }
    if (changes.length === 0) {
      render(settingsView, scope);
      targetStatus.textContent = hostChanges.length ? "Applying host changes…" : resetRequests.length === 1 ? "Defaults restored." : resetRequests.length || saveNotifications ? settingsSaveMessage(true, nodeAlertsOff(scope, payload, settingsView)) : "No changes.";
      if (hostChanges.length) {
        hostFailures = [];
        hostSkipped = [];
        hostQueue = hostChanges;
        submitNextHostChange();
      }
      return;
    }
    render(settingsView, scope);
    submittedPortNode = scope;
    try {
      const portView = await requestPortControl("POST", { changes });
      if (/^[0-9a-f]{32}$/.test(String(portView?.status?.id || ""))) {
        portRequestID = portView.status.id;
        try { localStorage.setItem("nodeStackPortRequest", portRequestID); } catch {}
      }
      renderPortControl(portView);
      targetStatus.textContent = portStatusMessage(portControlModel(portView)) || settingsSaveMessage(saveNotifications, nodeAlertsOff(scope, payload, settingsView));
    } catch (error) {
      applyPortDraft(draft);
      const prefix = saveNotifications ? "Settings saved, but " : "";
      if (error.definitive) {
        submittedPortNode = "";
        targetStatus.textContent = `${prefix}LAN port changes were rejected: ${error.message}`;
      } else {
        ambiguousPortDraft = { draft, changes };
        ambiguousStatusPrefix = saveNotifications ? "Settings saved. " : "";
        ambiguousStatusTarget = targetStatus;
        ambiguousAlerts = { scope, payload };
        targetStatus.textContent = `${ambiguousStatusPrefix}Checking whether the restart was queued…`;
        loadPortControl();
      }
    }
  };
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    saveScope("global", form, status, save);
  });
  for (const [node, view] of Object.entries(nodeViews)) {
    view.form.addEventListener("submit", (event) => {
      event.preventDefault();
      saveScope(node, view.form, view.status, view.save);
    });
  }
  reset.addEventListener("click", () => {
    if (!allowed || !saved.defaults) return;
    resetStaged = true;
    renderGlobalFields(saved.defaults);
    token.value = saved.defaults.telegram_bot_token || "";
    chat.value = saved.defaults.telegram_chat_id || "";
    url.value = saved.defaults.healthchecks_url || "";
    hostStaged = {};
    for (const [action, input] of Object.entries(hostToggles)) {
      if (input.disabled) continue;
      hostDraft[action] = hostToggleDefaults[action];
      input.checked = hostToggleDefaults[action];
    }
    renderHostActions();
    updateInputs();
    status.textContent = "Defaults filled in. Save applies them; Cancel keeps your current settings.";
  });
  fetch("/api/settings?scope=appearance")
    .then((response) => response.ok ? response.json() : Promise.reject())
    .then(applyAppearance)
    .catch(() => applyAppearance({}));
  credentialSettings.start();
  load("nodes");
}
