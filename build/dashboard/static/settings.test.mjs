import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { notificationPayload, notificationChanged, configurationAccess, configuredNodeVisibility, portControlModel, portChanges, portCollision, portStatusMessage, portStatusNode, portDraftAfterStatus, portSubmissionOutcome, shouldPollPort, settingsSaveMessage, normalizeDefaultCurrency, applyAppearance, requestSettings, scopedSettingsPayload, splitNodeControlSections, alertPreferenceModel, hostControlModel, hostActionPayload, bootControlLabel, newlyInstalledNodes, shouldPollHost, latestRequestGate } from "./settings.js";

const alertDefaults = {
  alert_monitor_online: true,
  alert_node_down: true,
  alert_node_recovered: true,
  alert_sync_complete: true,
  alert_low_disk: true,
  alert_updates: true,
  node_down_after_minutes: 3,
  low_disk_gib: 50,
};

test("settings remain read-only unless the server confirms configuration access", () => {
  assert.equal(configurationAccess({ can_configure: true }), true);
  for (const data of [undefined, {}, { can_configure: false }, { can_configure: "true" }]) {
    assert.equal(configurationAccess(data), false);
  }
});

test("host controls expose installed nodes, platform startup wording, and revision-bound config approval", () => {
  const revision = "a".repeat(64);
  const view = hostControlModel({
    available: true, install_supported: true, boot_supported: true, boot_enabled: true, boot_kind: "login",
    config_pending: true, config_revision: revision,
    config_path: "/etc/node-manager/config.json",
    nodes: [{ id: "bitcoin", name: "Bitcoin", installed: true, data_dir: "/var/lib/node-manager/data/bitcoin", disk_estimate: "28–56 GiB", disk_note: "Estimate", interfaces: [{ id: "bitcoin_rpc", name: "RPC", host_port: 8332 }] }, { id: "unknown", installed: true }],
    status: { action: "install", state: "pending", message: "Installing…" },
  });
  assert.equal(view.nodes.bitcoin.installed, true);
  assert.equal(view.nodes.monero.installed, false);
  assert.equal(view.config_pending, true);
  assert.equal(view.boot_kind, "login");
  assert.equal(view.config_path, "/etc/node-manager/config.json");
  assert.deepEqual(view.nodes.bitcoin.interfaces, [{ id: "bitcoin_rpc", name: "RPC", enabled: false, host_port: 8332 }]);
  assert.equal(bootControlLabel(view.boot_kind), "Launch at login");
  assert.deepEqual(hostActionPayload("install", "monero"), { action: "install", node: "monero" });
  assert.deepEqual(hostActionPayload("install", "monero", { data_dir: "/srv/monero", interfaces: [
    { id: "monero_rpc", enabled: false, host_port: 18081 }, { id: "monero_zmq", enabled: true, host_port: 28083 },
  ] }), { action: "install", node: "monero", data_dir: "/srv/monero", interfaces: [
    { id: "monero_rpc", enabled: false, host_port: 18081 }, { id: "monero_zmq", enabled: true, host_port: 28083 },
  ] });
  assert.equal(hostActionPayload("install", "monero", { data_dir: "../monero", interfaces: [] }), undefined);
  assert.equal(hostActionPayload("install", "monero", { data_dir: "/srv/monero", interfaces: [{ id: "bitcoin_rpc", enabled: true, host_port: 8332 }] }), undefined);
  assert.deepEqual(hostActionPayload("launch_at_boot", false), { action: "launch_at_boot", enabled: false });
  assert.deepEqual(hostActionPayload("apply_config", revision), { action: "apply_config", config_revision: revision });
  assert.equal(hostActionPayload("install", "unknown"), undefined);
  assert.equal(hostActionPayload("apply_config", "bad"), undefined);
});

test("completed installs reveal each new node without treating pending work as installed", () => {
  const empty = hostControlModel({ nodes: [] });
  const pending = hostControlModel({
    nodes: [], status: { action: "install", state: "pending", message: "Installing Bitcoin…" },
  });
  const bitcoin = hostControlModel({
    nodes: [{ id: "bitcoin", installed: true }],
    status: { action: "install", state: "applied", message: "Bitcoin installed." },
  });
  const bitcoinAndMonero = hostControlModel({
    nodes: [{ id: "bitcoin", installed: true }, { id: "monero", installed: true }],
    status: { action: "install", state: "applied", message: "Monero installed." },
  });

  assert.deepEqual(newlyInstalledNodes(undefined, empty), []);
  assert.deepEqual(newlyInstalledNodes(empty, pending), []);
  assert.equal(shouldPollHost(pending), true);
  assert.deepEqual(newlyInstalledNodes(pending, bitcoin), ["bitcoin"]);
  assert.equal(shouldPollHost(bitcoin), false);
  assert.deepEqual(newlyInstalledNodes(bitcoin, bitcoinAndMonero), ["monero"]);
});

test("a host mutation ignores an older settings response", async () => {
  const gate = latestRequestGate();
  const rendered = [];
  let resolveOld;
  let rejectOld;
  const oldRequest = gate.run(
    () => new Promise((resolve) => { resolveOld = resolve; }),
    (value) => rendered.push(value),
    (error) => rendered.push(error.message),
  );
  const oldFailure = gate.run(
    () => new Promise((resolve, reject) => { rejectOld = reject; }),
    (value) => rendered.push(value),
    (error) => rendered.push(error.message),
  );

  gate.invalidate();
  resolveOld("stale boot state");
  rejectOld(new Error("stale poll error"));
  assert.equal(await oldRequest, false);
  assert.equal(await oldFailure, false);
  assert.deepEqual(rendered, []);

  assert.equal(await gate.run(
    async () => "current boot state",
    (value) => rendered.push(value),
    (error) => rendered.push(error.message),
  ), true);
  assert.deepEqual(rendered, ["current boot state"]);
});

test("only the newest host response is displayed", async () => {
  const gate = latestRequestGate();
  const rendered = [];
  let resolveOld;
  const oldRequest = gate.run(
    () => new Promise((resolve) => { resolveOld = resolve; }),
    (value) => rendered.push(value),
    (error) => rendered.push(error.message),
  );
  await gate.run(async () => "new state", (value) => rendered.push(value), (error) => rendered.push(error.message));
  resolveOld("old state");
  assert.equal(await oldRequest, false);
  assert.deepEqual(rendered, ["new state"]);
});


test("global alert preferences default on and accept only bounded thresholds", () => {
  assert.deepEqual(alertPreferenceModel(), alertDefaults);
  assert.deepEqual(alertPreferenceModel({
    alert_node_down: false,
    alert_updates: false,
    node_down_after_minutes: "12",
    low_disk_gib: 250,
  }), { ...alertDefaults, alert_node_down: false, alert_updates: false, node_down_after_minutes: 12, low_disk_gib: 250 });
  assert.deepEqual(alertPreferenceModel({ node_down_after_minutes: 0, low_disk_gib: 1048577 }), alertDefaults);
});

test("a port-only save does not create an unnecessary notification override", () => {
  const saved = {
    telegram_enabled: false, telegram_chat_id: "", healthchecks_enabled: false,
    alert_new_block: false, alert_new_block_bitcoin: false, alert_new_block_monero: false, alert_new_block_tari:false, alert_new_block_litecoin:false, alert_new_block_dogecoin:false, alert_new_block_bitcoincash:false, alert_new_block_zcash:false, tower_labels: false, computer_stats: false, default_currency: "USD",
  };
  assert.equal(notificationChanged(saved, notificationPayload({})), false);
  assert.equal(notificationChanged(saved, notificationPayload({ alert_new_block_monero: true })), true);
  assert.equal(notificationChanged(saved, notificationPayload({ default_currency: "CNY" })), true);
});

test("node-specific settings show only configured nodes", () => {
  assert.deepEqual(configuredNodeVisibility({ bitcoin_enabled: true, monero_enabled: false }), { bitcoin: true, monero: false, tari:false, litecoin:false, dogecoin:false, bitcoincash:false, zcash:false });
  assert.deepEqual(configuredNodeVisibility({ bitcoin_enabled: false, monero_enabled: true }), { bitcoin: false, monero: true, tari:false, litecoin:false, dogecoin:false, bitcoincash:false, zcash:false });
  assert.deepEqual(configuredNodeVisibility({ bitcoin_enabled: "true", monero_enabled: 1 }), { bitcoin: false, monero: false, tari:false, litecoin:false, dogecoin:false, bitcoincash:false, zcash:false });
});

test("port controls expose only typed configured ports and trusted states", () => {
  assert.deepEqual(portControlModel({ available: true, ports: [
    { id: "bitcoin_rpc", enabled: true, bind_ip: "192.168.1.2", host_port: 8332 },
    { id: "unknown", enabled: true },
  ], status: { state: "pending", message: "Applying LAN port changes…", affected_services: ["tor", "dashboard", "bitcoin"] } }), {
    available: true,
    ports: { bitcoin_rpc: { id: "bitcoin_rpc", enabled: true, bind_ip: "192.168.1.2", host_port: 8332 } },
    state: "pending",
    message: "Applying LAN port changes…",
    affected_services: ["tor", "dashboard"],
    error_code: "",
    port: "",
  });
  assert.deepEqual(portControlModel({ available: "true", status: { state: "invented", message: "ignored" } }), {
    available: false, ports: {}, state: "", message: "", affected_services: [], error_code: "", port: "",
  });
});

test("port changes stay staged and never send an editable bind address", () => {
  const ports = {
    bitcoin_rpc: { id: "bitcoin_rpc", enabled: true, bind_ip: "192.168.1.2", host_port: 8332 },
    monero_rpc: { id: "monero_rpc", enabled: false, bind_ip: "192.168.1.2", host_port: 18081 },
  };
  assert.deepEqual(portChanges(ports, {
    bitcoin_rpc: { enabled: false, host_port: 8332 },
    monero_rpc: { enabled: true, host_port: "28081" },
  }), [
    { port: "bitcoin_rpc", enabled: false },
    { port: "monero_rpc", enabled: true, host_port: 28081 },
  ]);
  assert.deepEqual(portChanges(ports, ports), []);
});

test("port collisions name the affected listener", () => {
  const draft = {
    bitcoin_rpc: { enabled: true, host_port: 8332 },
    monero_rpc: { enabled: true, host_port: 8332 },
  };
  const sameBind = {
    bitcoin_rpc: { bind_ip: "192.168.1.2" },
    monero_rpc: { bind_ip: "192.168.1.2" },
  };
  assert.equal(portCollision([{ port: "monero_rpc", enabled: true, host_port: 8332 }], draft, sameBind), "monero_rpc");
  assert.equal(portCollision([{ port: "monero_rpc", enabled: true, host_port: 8332 }], draft, {
    ...sameBind, monero_rpc: { bind_ip: "192.168.1.3" },
  }), "");
  assert.equal(portStatusMessage({ error_code: "port_in_use", port: "monero_rpc" }), "Selected port for Monero RPC is already in use.");
});

test("a rejected helper result preserves the user's port draft", () => {
  const draft = { monero_rpc: { enabled: true, host_port: 28081 } };
  assert.equal(portDraftAfterStatus(draft, "rejected", true), draft);
  assert.equal(portDraftAfterStatus(draft, "failed", true), draft);
  assert.equal(portDraftAfterStatus(draft, "applied", false), draft);
  assert.equal(portDraftAfterStatus(draft, "applied", true), undefined);
});

test("a lost port POST response reconciles without claiming the request failed", () => {
  const changes = [{ port: "bitcoin_rpc", enabled: true, host_port: 8332 }];
  assert.equal(portSubmissionOutcome({ state: "pending", ports: {} }, changes), "queued");
  assert.equal(portSubmissionOutcome({ state: "", ports: {
    bitcoin_rpc: { enabled: true, host_port: 8332 },
  } }, changes), "applied");
  assert.equal(portSubmissionOutcome({ state: "", ports: {
    bitcoin_rpc: { enabled: false, host_port: 8332 },
  } }, changes), "unknown");
  assert.equal(portSubmissionOutcome({ state: "", ports: {} }, changes), "unknown");
});

test("port status polling stops when the settings dialog closes", () => {
  assert.equal(shouldPollPort(true, "pending"), true);
  assert.equal(shouldPollPort(false, "pending"), false);
  assert.equal(shouldPollPort(true, "failed"), false);
});

test("port progress stays in the live port status", () => {
  assert.equal(settingsSaveMessage(false), "");
  assert.equal(settingsSaveMessage(true), "Settings saved.");
  assert.equal(portStatusNode("", "monero", []), "monero");
  assert.equal(portStatusNode("litecoin_rpc", "monero", []), "litecoin");
  assert.equal(portStatusNode("", "", ["zcash"]), "zcash");
});

test("blank secret fields keep saved secrets without copying them into the request", () => {
  assert.deepEqual(notificationPayload({ telegram_enabled: true, telegram_chat_id: " -10042 ",
    healthchecks_enabled: true, alert_new_block_bitcoin: false, alert_new_block_monero: true }), {
    telegram_enabled: true, telegram_chat_id: "-10042", healthchecks_enabled: true, alert_new_block: false,
    alert_new_block_monero: true,
    alert_new_block_tari:false, alert_new_block_litecoin:false, alert_new_block_dogecoin:false, alert_new_block_bitcoincash:false, alert_new_block_zcash:false,
    tower_labels: false,
    computer_stats: false,
    default_currency: "USD",
    ...alertDefaults,
  });
});

test("disabled services do not send stale secrets or chat IDs", () => {
  assert.deepEqual(notificationPayload({ telegram_enabled: false, telegram_chat_id: "42",
    telegram_bot_token: "not-a-real-token", healthchecks_enabled: false,
    healthchecks_url: "https://example.com/not-a-real-ping", alert_new_block_bitcoin: true }), {
    telegram_enabled: false, telegram_chat_id: "", healthchecks_enabled: false, alert_new_block: true,
    alert_new_block_monero: false,
    alert_new_block_tari:false, alert_new_block_litecoin:false, alert_new_block_dogecoin:false, alert_new_block_bitcoincash:false, alert_new_block_zcash:false,
    tower_labels: false,
    computer_stats: false,
    default_currency: "USD",
    ...alertDefaults,
  });
});

test("appearance defaults are applied and broadcast together", () => {
  const root = { dataset: {} };
  const events = [];
  const target = { dispatchEvent: (event) => events.push(event) };
  assert.deepEqual(applyAppearance({}, root, target), { tower_labels: false, computer_stats: false, default_currency: "USD" });
  assert.equal(root.dataset.towerLabels, "0");
  assert.equal(root.dataset.computerStats, "0");
  assert.deepEqual(applyAppearance({ tower_labels: true, computer_stats: true, default_currency: "cny" }, root, target), { tower_labels: true, computer_stats: true, default_currency: "CNY" });
  assert.equal(root.dataset.towerLabels, "1");
  assert.equal(root.dataset.computerStats, "1");
  assert.equal(events.at(-1).type, "appearance-updated");
  assert.deepEqual(events.at(-1).detail, { tower_labels: true, computer_stats: true, default_currency: "CNY" });
  assert.equal(normalizeDefaultCurrency("btc"), "USD");
});

test("replacement secrets are sent only for enabled services", () => {
  const payload = notificationPayload({ telegram_enabled: true, telegram_chat_id: "42",
    telegram_bot_token: " sample-token ", healthchecks_enabled: true,
    healthchecks_url: " https://example.com/ping ", alert_new_block_bitcoin: true, alert_new_block_monero: false });
  assert.equal(payload.telegram_bot_token, "sample-token");
  assert.equal(payload.healthchecks_url, "https://example.com/ping");
});

test("node and global saves use independent HTTP scopes and payloads", async () => {
  const requests = [];
  const fetcher = async (url, options) => {
    requests.push({ url, method: options.method, body: options.body ? JSON.parse(options.body) : undefined });
    return { ok: true, json: async () => ({ can_configure: true }) };
  };
  await requestSettings("bitcoin", "GET", undefined, fetcher);
  const nodePayload = scopedSettingsPayload("monero", { alert_new_block_monero: true, telegram_bot_token: "must-not-leak" });
  await requestSettings("monero", "POST", nodePayload, fetcher);
  const globalPayload = scopedSettingsPayload("global", {
    telegram_enabled: true,
    telegram_bot_token: "replacement-token",
    telegram_chat_id: "42",
    healthchecks_enabled: true,
    healthchecks_url: "https://example.com/ping",
    tower_labels: true,
    default_currency: "CNY",
    alert_new_block_monero: true,
    alert_monitor_online: false,
    alert_node_down: true,
    alert_node_recovered: false,
    alert_sync_complete: true,
    alert_low_disk: false,
    alert_updates: true,
    node_down_after_minutes: 8,
    low_disk_gib: 75,
  });
  await requestSettings("global", "POST", globalPayload, fetcher);
  assert.deepEqual(requests[0], { url: "/api/settings?scope=nodes", method: "GET", body: undefined });
  assert.deepEqual(requests[1], {
    url: "/api/settings?scope=nodes", method: "POST", body: { alert_new_block_monero: true },
  });
  assert.equal(requests[2].url, "/api/settings?scope=global");
  assert.equal(requests[2].body.telegram_bot_token, "replacement-token");
  assert.equal(requests[2].body.healthchecks_url, "https://example.com/ping");
  assert.equal(requests[2].body.tower_labels, true);
  assert.equal(requests[2].body.default_currency, "CNY");
  assert.deepEqual(Object.fromEntries(Object.keys(alertDefaults).map((field) => [field, requests[2].body[field]])), {
    alert_monitor_online: false,
    alert_node_down: true,
    alert_node_recovered: false,
    alert_sync_complete: true,
    alert_low_disk: false,
    alert_updates: true,
    node_down_after_minutes: 8,
    low_disk_gib: 75,
  });
  assert.equal(Object.keys(requests[2].body).some((field) => field.startsWith("alert_new_block")), false);
  await assert.rejects(
    requestSettings("bitcoin", "POST", { alert_new_block: true }, async () => ({ ok: false, text: async () => "node settings rejected" })),
    /node settings rejected/,
  );
});

test("node controls split alerts from LAN access and stay ahead of deactivation", async () => {
  const html = await readFile(new URL("../index.html", import.meta.url), "utf8");
  const source = await readFile(new URL("./settings.js", import.meta.url), "utf8");
  const ports = [{ className: "settings-port" }, { className: "settings-port" }];
  const alerts = {
    summary: { textContent: "Bitcoin" },
    querySelector(selector) { return selector === "summary" ? this.summary : undefined; },
    querySelectorAll(selector) { return selector === ".settings-port" ? ports : []; },
  };
  const target = { createElement(tag) {
    const element = { tag, className: "", textContent: "", children: [], append(...children) { this.children.push(...children); } };
    return element;
  } };
  const lan = splitNodeControlSections(alerts, target);
  assert.equal(alerts.summary.textContent, "Alerts");
  assert.equal(lan.className, "node-lan-settings");
  assert.equal(lan.children[0].textContent, "LAN access");
  assert.deepEqual(lan.children.slice(1), ports);
  assert.match(html, /<fieldset id="settings-node-alerts" hidden>/);
  assert.doesNotMatch(source, /node-settings-card|Alerts and LAN access|power\.before/);
  assert.match(source, /nodeForm\.className = "node-settings-form"/);
  assert.match(source, /nodeForm\.dataset\.nodeSettingsForm = node/);
  assert.match(source, /nodeForm\.append\(details, lan, nodeStatus, impact, actions\)/);
  assert.match(source, /power\.querySelector\("h2"\)\.after\(nodeForm\)/);
  assert.match(source, /saveScope\(node, view\.form, view\.status, view\.save\)/);
});

test("every node shows its approximate maximum alert volume and coalescing behavior", async () => {
  const html = await readFile(new URL("../index.html", import.meta.url), "utf8");
  const estimates = { bitcoin: 144, monero: 720, tari: 720, litecoin: 576, dogecoin: 1440, bitcoincash: 144, zcash: 1152 };
  for (const [node, count] of Object.entries(estimates)) {
    assert.match(html, new RegExp(`id="settings-${node}-new-block"[^>]*> Alert on new blocks \\(up to about ${count} alerts/day\\)`));
  }
  assert.equal(html.match(/Several blocks found together share one alert\./g)?.length, 7);
});
