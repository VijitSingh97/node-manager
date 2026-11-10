import { credentialSettingsController } from "./credentials.js";
import { dashboardFetch as fetch } from "./session.js";

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
    default_currency: normalizeDefaultCurrency(values.default_currency),
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

export function hostControlModel(data = {}) {
  const nodes = Object.fromEntries(allNodes.map((id) => [id, { id, name: id, installed: false, data_dir: "", disk_estimate: "", disk_note: "", interfaces: [] }]));
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
      data_dir: typeof node.data_dir === "string" && node.data_dir.startsWith("/") ? node.data_dir : "",
      disk_estimate: String(node.disk_estimate || ""),
      disk_note: String(node.disk_note || ""),
      interfaces,
    };
  }
  const action = ["install", "launch_at_boot", "apply_config"].includes(data?.status?.action) ? data.status.action : "";
  const state = ["pending", "applied", "rejected", "failed"].includes(data?.status?.state) ? data.status.state : "";
  const bootKind = ["boot", "login", "unsupported"].includes(data.boot_kind) ? data.boot_kind : "unsupported";
  const revision = data.config_pending === true && /^[0-9a-f]{64}$/.test(String(data.config_revision || "")) ? data.config_revision : "";
  return {
    available: data.available === true,
    install_supported: data.install_supported === true,
    boot_supported: data.boot_supported === true && bootKind !== "unsupported",
    boot_enabled: data.boot_enabled === true,
    boot_kind: bootKind,
    config_pending: !!revision,
    config_revision: revision,
    config_path: typeof data.config_path === "string" && data.config_path.startsWith("/") ? data.config_path : "",
    nodes,
    status: { action, state, message: state ? String(data.status?.message || "") : "" },
  };
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
    return { action, node: value, data_dir: dataDir, interfaces };
  }
  if (action === "launch_at_boot" && typeof value === "boolean") return { action, enabled: value };
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

export function settingsSaveMessage(settingsSaved) {
  return settingsSaved ? "Settings saved." : "";
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
  const defaultCurrency = byId("default-currency");
  const save = byId("save");
  const reset = byId("reset");
  const hostNodes = byId("host-nodes");
  const launchAtStart = byId("launch-at-start");
  const launchAtStartLabel = byId("launch-at-start-label");
  const configChange = byId("config-change");
  const applyConfig = byId("apply-config");
  const hostStatus = byId("host-status");
  const configPath = byId("config-path");
  const installDialog = byId("install-dialog");
  const installForm = byId("install-form");
  const installTitle = byId("install-title");
  const installDisk = byId("install-disk");
  const installDataDir = byId("install-data-dir");
  const installInterfaces = byId("install-interfaces");
  const installError = byId("install-error");
  const installSubmit = byId("install-submit");
  const nodeViews = {};
  for (const node of allNodes) {
    const details = byId(`${node}-alerts`);
    const panel = document.querySelector(`[data-node-panel="${node}"]`);
    const power = panel?.querySelector(".node-power-card");
    if (!details || !power) continue;
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
    nodeSave.textContent = "Save";
    actions.append(nodeSave);
    nodeForm.append(details, lan, nodeStatus, impact, actions);
    power.querySelector("h2").after(nodeForm);
    nodeViews[node] = { form: nodeForm, status: nodeStatus, impact, save: nodeSave };
  }
  byId("port-status")?.remove();
  byId("port-impact")?.remove();
  const credentialSettings = credentialSettingsController(dialog, document);
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
  let submittedPortNode = "";
  let stagedPortDraft;
  let hostPoll;
  let hostView = hostControlModel();
  let hostLoaded = false;
  let pendingBootEnabled;
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
    reset.disabled = !allowed || pending || !saved.overridden;
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
    if (pendingBootEnabled !== undefined) {
      const failed = nextHostView.status.action === "launch_at_boot" && ["rejected", "failed"].includes(nextHostView.status.state);
      if (failed || nextHostView.boot_enabled === pendingBootEnabled) pendingBootEnabled = undefined;
      else {
        nextHostView.boot_enabled = pendingBootEnabled;
        nextHostView.status = { action: "launch_at_boot", state: "pending", message: hostView.status.message || "Applying host change…" };
      }
    }
    const installed = hostLoaded ? newlyInstalledNodes(hostView, nextHostView) : [];
    hostView = nextHostView;
    hostLoaded = true;
    hostNodes.replaceChildren();
    const busy = hostView.status.state === "pending";
    for (const node of Object.values(hostView.nodes)) {
      const row = document.createElement("div");
      row.className = "settings-host-node";
      const name = document.createElement("span");
      name.textContent = node.name;
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = node.installed ? "Installed" : "Install";
      button.disabled = node.installed || !node.data_dir || !hostView.available || !hostView.install_supported || hostView.config_pending || busy;
      button.addEventListener("click", () => openInstall(node.id));
      row.append(name, button);
      hostNodes.append(row);
    }
    launchAtStart.checked = hostView.boot_enabled;
    launchAtStart.disabled = !hostView.available || !hostView.boot_supported || hostView.config_pending || busy;
    launchAtStartLabel.querySelector("span").textContent = bootControlLabel(hostView.boot_kind);
    launchAtStartLabel.hidden = hostView.boot_kind === "unsupported";
    configChange.hidden = !hostView.config_pending;
    applyConfig.disabled = !hostView.available || busy;
    hostStatus.textContent = hostView.status.message || (!hostView.available ? "Host controls require the installed helper." : "");
    configPath.textContent = hostView.config_path || "Unavailable until the installed helper reports it.";
    clearTimeout(hostPoll);
    if (shouldPollHost(hostView)) hostPoll = setTimeout(loadHost, 2000);
    if (installed.length) window.dispatchEvent(new CustomEvent("node-catalog-updated", { detail: { installed } }));
  };

  const openInstall = (nodeID) => {
    const node = hostView.nodes[nodeID];
    if (!node || node.installed || !node.data_dir) return;
    installNode = nodeID;
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
    installError.textContent = "";
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
    hostStatus.textContent = "Applying…";
    for (const button of hostNodes.querySelectorAll("button")) button.disabled = true;
    launchAtStart.disabled = applyConfig.disabled = true;
    if (payload.action === "launch_at_boot") pendingBootEnabled = payload.enabled;
    try {
      const statusView = await requestHost(payload);
      hostView.status = { action: payload.action, state: "pending", message: statusView.message || "Applying host change…" };
      hostStatus.textContent = hostView.status.message;
      hostPoll = setTimeout(loadHost, 1000);
    } catch (error) {
      if (payload.action === "launch_at_boot") pendingBootEnabled = undefined;
      hostStatus.textContent = error.message;
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
  const render = (data, scope = "global") => {
    saved = { ...saved, ...data };
    allowed = configurationAccess(data);
    if (Object.hasOwn(data, "dashboard_password_set")) credentialSettings.setPasswordConfigured(data.dashboard_password_set === true);
    if (scope === "global") {
      telegramEnabled.checked = !!data.telegram_enabled;
      healthchecksEnabled.checked = !!data.healthchecks_enabled;
      const preferences = alertPreferenceModel(data);
      for (const [field, input] of Object.entries(alertPreferences)) input.checked = preferences[field];
      nodeDownAfterMinutes.value = preferences.node_down_after_minutes;
      lowDiskGiB.value = preferences.low_disk_gib;
    }
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
    if (scope === "global") {
      towerLabels.checked = !!data.tower_labels;
      computerStats.checked = !!data.computer_stats;
      defaultCurrency.value = normalizeDefaultCurrency(data.default_currency);
      applyAppearance(data);
      if (Object.hasOwn(data, "telegram_chat_id")) chat.value = data.telegram_chat_id || "";
      if (dialog.open) {
        if (Object.hasOwn(data, "telegram_bot_token")) token.value = data.telegram_bot_token || "";
        if (Object.hasOwn(data, "healthchecks_url")) url.value = data.healthchecks_url || "";
      } else clearSecrets();
    }
    token.placeholder = "Paste your bot token";
    url.placeholder = "https://hc-ping.com/…";
    reset.disabled = !allowed || !data.overridden;
    telegramEnabled.disabled = healthchecksEnabled.disabled = bitcoinNewBlock.disabled = moneroNewBlock.disabled = towerLabels.disabled = computerStats.disabled = defaultCurrency.disabled = !allowed;
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
          (ambiguousStatusTarget || status).textContent = settingsSaveMessage(ambiguousStatusPrefix !== "");
        } else if (outcome === "applied") {
          ambiguousPortDraft = undefined;
          stagedPortDraft = undefined;
          updatePortPlan(false);
          (ambiguousStatusTarget || status).textContent = settingsSaveMessage(ambiguousStatusPrefix !== "");
        } else {
          ambiguousPortDraft = undefined;
          applyPortDraft(draft);
          (ambiguousStatusTarget || status).textContent = `${ambiguousStatusPrefix}The restart request status is unknown; review the LAN port values before trying again.`;
        }
        ambiguousStatusPrefix = "";
        ambiguousStatusTarget = undefined;
      }
    }
    catch { if (dialog.open || document.body.dataset.node) portPoll = setTimeout(loadPortControl, 2000); }
  }
  async function load(scope = "global") {
    allowed = false;
    save.disabled = reset.disabled = true;
    telegramEnabled.disabled = healthchecksEnabled.disabled = bitcoinNewBlock.disabled = moneroNewBlock.disabled = towerLabels.disabled = computerStats.disabled = defaultCurrency.disabled = true;
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
  byId("open").addEventListener("click", () => { dialog.showModal(); credentialSettings.openPassword(); load(); loadHost(); });
  byId("close").addEventListener("click", () => dialog.close());
  byId("install-cancel").addEventListener("click", () => installDialog.close());
  installForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const interfaces = Array.from(installInterfaces.children).map((row) => ({
      id: row.dataset.interface,
      enabled: row.querySelector("[data-interface-enabled]").checked,
      host_port: Number(row.querySelector("[data-interface-port]").value),
    }));
    const payload = hostActionPayload("install", installNode, { data_dir: installDataDir.value.trim(), interfaces });
    if (!payload) {
      installError.textContent = "Choose an absolute host folder and valid ports from 1 to 65535.";
      return;
    }
    installSubmit.disabled = true;
    installDialog.close();
    submitHost(payload).finally(() => { installSubmit.disabled = false; });
  });
  dialog.addEventListener("close", () => { clearSecrets(); credentialSettings.closePassword(); delete saved.telegram_bot_token; delete saved.telegram_chat_id; delete saved.healthchecks_url; });
  window.addEventListener("node-selected", (event) => {
    credentialSettings.closeNode();
    if (event.detail?.node) {
      credentialSettings.openNode();
      load("nodes");
    }
  });
  telegramEnabled.addEventListener("change", updateInputs);
  healthchecksEnabled.addEventListener("change", updateInputs);
  launchAtStart.addEventListener("change", () => submitHost(hostActionPayload("launch_at_boot", launchAtStart.checked)));
  applyConfig.addEventListener("click", () => submitHost(hostActionPayload("apply_config", hostView.config_revision)));
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
      default_currency: defaultCurrency.value,
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

  const saveScope = async (scope, scopeForm, targetStatus, targetSave) => {
    if (!allowed) return;
    const allChanges = updatePortPlan(true);
    const changes = scope === "global" ? [] : changesForNode(scope, allChanges);
    const draft = portDraft();
    const portPlanValid = validatePortPlan(changes);
    const formValid = scopeForm.reportValidity();
    if (!portPlanValid || !formValid) return;
    const payload = settingsPayload(scope);
    const saveNotifications = notificationChanged(saved, payload);
    if (!saveNotifications && changes.length === 0) {
      targetStatus.textContent = "No changes.";
      return;
    }
    targetSave.disabled = true;
    if (scope === "global") reset.disabled = true;
    targetStatus.textContent = "Saving…";
    let settingsView = saved;
    if (saveNotifications) {
      try {
        settingsView = await request("POST", payload, scope);
      } catch (error) {
        clearSecrets();
        targetStatus.textContent = error.message;
        targetSave.disabled = !allowed;
        reset.disabled = !allowed || !saved.overridden;
        updateInputs();
        updatePortPlan();
        return;
      }
    }
    if (changes.length === 0) {
      render(settingsView, scope);
      targetStatus.textContent = "Settings saved.";
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
      targetStatus.textContent = portStatusMessage(portControlModel(portView)) || settingsSaveMessage(saveNotifications);
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
  reset.addEventListener("click", async () => {
    if (!allowed) return;
    save.disabled = reset.disabled = true;
    status.textContent = "Saving…";
    try {
      render(await request("POST", { reset: true }), "global");
      status.textContent = "Using the server configuration.";
    } catch (error) {
      status.textContent = error.message;
      save.disabled = !allowed;
      reset.disabled = !allowed || !saved.overridden;
    }
  });
  fetch("/api/settings?scope=appearance")
    .then((response) => response.ok ? response.json() : Promise.reject())
    .then(applyAppearance)
    .catch(() => applyAppearance({}));
  credentialSettings.start();
  load("nodes");
}
