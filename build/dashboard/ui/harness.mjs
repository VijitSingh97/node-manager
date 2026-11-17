import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { createServer } from "node:http";
import { createServer as createNetServer } from "node:net";
import { existsSync, mkdtempSync, readFileSync, renameSync, rmSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";
import { findChrome, launchChrome } from "./cdp.mjs";

const DASHBOARD_DIR = join(dirname(fileURLToPath(import.meta.url)), "..");
const PASSWORD = "ui-flows-password";
const children = new Set();
process.once("exit", () => children.forEach((child) => child.kill("SIGKILL")));
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export const defaultSettings = {
  telegram_enabled: false, telegram_bot_token: "", telegram_chat_id: "", healthchecks_enabled: false, healthchecks_url: "",
  alert_new_block: false, alert_new_block_monero: false, alert_new_block_tari: false, alert_new_block_litecoin: false,
  alert_new_block_dogecoin: false, alert_new_block_bitcoincash: false, alert_new_block_zcash: false,
  alert_monitor_online: true, alert_node_down: true, alert_node_recovered: true, alert_sync_complete: true,
  alert_low_disk: true, alert_updates: true, node_down_after_minutes: 3, low_disk_gib: 50,
  tower_labels: false, computer_stats: false, thermal_throttle: false, default_currency: "USD", actuator_health: false, actuator_info: false,
};

export const nodeIDs = ["bitcoin", "monero", "tari", "litecoin", "dogecoin", "bitcoincash", "zcash"];
const dataPaths = Object.fromEntries(nodeIDs.map((id) => [id, `/srv/node/${id}`]));

export const baseCapability = {
  platform: "linux", boot_kind: "boot", install_supported: true, migrate_supported: true, boot_supported: true, boot_enabled: false,
  clearnet_over_tor: true, inbound_connections: true, utxo_index: false, utxo_index_litecoin: false, config_pending: false,
  config_revision: "", config_path: "/etc/node-manager/config.json", data_paths: dataPaths,
};

let dashboardBinary;
function resolveBinary(env = process.env) {
  if (dashboardBinary !== undefined) return dashboardBinary;
  if (env.DASHBOARD_BIN) return (dashboardBinary = existsSync(env.DASHBOARD_BIN) ? resolve(env.DASHBOARD_BIN) : "");
  const out = mkdtempSync(join(tmpdir(), "ui-dashboard-bin-"));
  process.once("exit", () => rmSync(out, { recursive: true, force: true }));
  try {
    execFileSync("go", ["build", "-o", join(out, "dashboard"), "."], { cwd: DASHBOARD_DIR, stdio: "pipe" });
    dashboardBinary = join(out, "dashboard");
  } catch {
    dashboardBinary = "";
  }
  return dashboardBinary;
}

export function skipReason(env = process.env) {
  const missing = [findChrome(env) ? "" : "Chrome (set CHROME)", resolveBinary(env) ? "" : "the dashboard binary (set DASHBOARD_BIN or install Go)"].filter(Boolean);
  if (!missing.length) return false;
  const message = `UI flow tests need ${missing.join(" and ")}`;
  if (env.CI === "true") throw new Error(message);
  return `${message}; skipping`;
}

const SKIP = skipReason();
let chrome;
const sharedChrome = async () => (chrome ??= await launchChrome(findChrome()));
after(async () => { await (await chrome)?.close(); });

function startRPC() {
  const result = {
    getblockchaininfo: { blocks: 123, headers: 123, verificationprogress: 1, initialblockdownload: false, pruned: true, prune_target_size: 2 ** 30, size_on_disk: 1e9, bestblockhash: "ab".repeat(32) },
    getnetworkinfo: { subversion: "/Satoshi:31.1.0/", connections: 3 },
    getpeerinfo: [{ inbound: true }, { inbound: false }],
    uptime: 90061,
    getmempoolinfo: { size: 42, bytes: 2 ** 20 },
    estimatesmartfee: { feerate: 0.00002 },
  };
  const server = createServer((request, response) => {
    let body = "";
    request.on("data", (chunk) => (body += chunk));
    request.on("end", () => {
      let call = {};
      try { call = JSON.parse(body); } catch {}
      const known = call.method in result;
      response.writeHead(known ? 200 : 500, { "Content-Type": "application/json" });
      response.end(JSON.stringify({ result: known ? result[call.method] : null, error: known ? null : { code: -32601 }, id: call.id ?? null }));
    });
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve(server)));
}

function freePort() {
  return new Promise((resolve) => {
    const probe = createNetServer().listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

function writeAtomic(path, content) {
  writeFileSync(`${path}.tmp`, content, { mode: 0o600 });
  renameSync(`${path}.tmp`, path);
}

class Helper {
  constructor(stateDir, capability) {
    this.stateDir = stateDir;
    this.capability = capability;
    this.requests = [];
    this.hold = false;
    this.outcome = () => "applied";
    this.timer = setInterval(() => this.#tick(), 5);
    this.keepalive = setInterval(() => this.writeCapability(), 10000);
    this.writeCapability();
  }

  writeCapability(patch = {}) {
    this.capability = { ...this.capability, ...patch };
    writeAtomic(join(this.stateDir, "node-control.capability"), JSON.stringify({ timestamp: Math.floor(Date.now() / 1000), ...this.capability }));
  }

  release() {
    this.hold = false;
  }

  #tick() {
    const path = join(this.stateDir, "node-control.request");
    if (this.hold || !existsSync(path)) return;
    let request;
    try { request = JSON.parse(readFileSync(path, "utf8")); } catch { return; }
    unlinkSync(path);
    this.requests.push(request);
    const state = this.outcome(request);
    const status = request.action
      ? { id: request.id, action: request.action, ...(request.node ? { node: request.node } : {}), ...(request.enabled === undefined ? {} : { enabled: request.enabled }), state }
      : { id: request.id, node: request.node, active: request.active, state };
    if (state === "applied") this.#apply(request);
    writeAtomic(join(this.stateDir, "node-control.status"), JSON.stringify(status));
  }

  #apply(request) {
    if (!request.action) {
      const path = join(this.stateDir, "node-runtime.json");
      const runtime = existsSync(path) ? JSON.parse(readFileSync(path, "utf8")) : {};
      writeAtomic(path, JSON.stringify({ ...runtime, [request.node]: request.active }));
      return;
    }
    const field = { launch_at_boot: "boot_enabled", clearnet_over_tor: "clearnet_over_tor", inbound_connections: "inbound_connections", utxo_index: request.node === "litecoin" ? "utxo_index_litecoin" : "utxo_index" }[request.action];
    if (field) this.writeCapability({ [field]: request.enabled });
    if (request.action === "apply_config") this.writeCapability({ config_pending: false, config_revision: "" });
  }

  stop() {
    clearInterval(this.timer);
    clearInterval(this.keepalive);
  }
}

export async function startDashboard({ nodes = [], settings, capability = {}, host = true, hostStats = false, password = PASSWORD } = {}) {
  const stateDir = mkdtempSync(join(tmpdir(), "ui-state-"));
  const rpc = await startRPC();
  const rpcURL = `http://127.0.0.1:${rpc.address().port}`;
  const port = await freePort();
  const env = {
    ...process.env, RPC_URL: rpcURL, RPC_USER: "uiuser", RPC_PASSWORD: "uirpc", DASHBOARD_PASSWORD: password, PORT: String(port),
    DATA_DIR: stateDir, STATE_DIR: stateDir, WATCH_STORE: join(stateDir, "wallets.json"), BALANCE_HISTORY: join(stateDir, "balance_history.json"),
    EVENTS_LOG: join(stateDir, "events.log"), WATCH_WALLETS_B64: "", TELEGRAM_BOT_TOKEN: "", TELEGRAM_CHAT_ID: "", HEALTHCHECKS_URL: "", STACK_VERSION: "1.0.0-ui",
  };
  for (const id of nodes) {
    const prefix = id.toUpperCase();
    env[`${prefix}_ENABLED`] = "1";
    env[`${prefix}_${id === "tari" ? "GRPC" : "RPC"}_URL`] = id === "tari" ? "http://127.0.0.1:1" : rpcURL;
    env[`${prefix}_RPC_USER`] = "uiuser";
    env[`${prefix}_RPC_PASSWORD`] = "uirpc";
  }
  if (settings) writeFileSync(join(stateDir, "notification_settings.json"), JSON.stringify({ ...defaultSettings, ...settings }), { mode: 0o600 });
  const log = [];
  const child = spawn(resolveBinary(), [], { cwd: DASHBOARD_DIR, env, stdio: ["ignore", "pipe", "pipe"] });
  children.add(child);
  child.stdout.on("data", (chunk) => log.push(String(chunk)));
  child.stderr.on("data", (chunk) => log.push(String(chunk)));
  const url = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + 15000;
  for (;;) {
    if (child.exitCode !== null) throw new Error(`dashboard exited: ${log.join("")}`);
    if (Date.now() > deadline) throw new Error(`dashboard did not become ready: ${log.join("")}`);
    try {
      if ((await fetch(`${url}/healthz`)).ok) break;
    } catch {}
    await sleep(25);
  }
  const helper = host ? new Helper(stateDir, { ...baseCapability, ...capability }) : undefined;
  const readJSON = (name) => (existsSync(join(stateDir, name)) ? JSON.parse(readFileSync(join(stateDir, name), "utf8")) : undefined);
  const dash = {
    url, stateDir, password, helper, readJSON,
    settings: () => readJSON("notification_settings.json"),
    writeState: (name, value) => writeAtomic(join(stateDir, name), JSON.stringify(value)),
    writeHostStats: (extra = {}) => writeAtomic(join(stateDir, "host-stats.json"), JSON.stringify({ updated: Math.floor(Date.now() / 1000), cpus: 4, mem_total_bytes: 8 * 2 ** 30, nodes: {}, other: { cpu_pct: 1, mem_bytes: 1 }, ...extra })),
    async stop() {
      helper?.stop();
      child.kill("SIGTERM");
      children.delete(child);
      await Promise.race([new Promise((resolve) => child.once("exit", resolve)), sleep(5000).then(() => child.kill("SIGKILL"))]);
      rpc.close();
      rpc.closeAllConnections();
      rmSync(stateDir, { recursive: true, force: true });
    },
  };
  if (hostStats) dash.writeHostStats(hostStats === true ? {} : hostStats);
  return dash;
}

export function expectSettings(dash, expected = {}) {
  assert.deepEqual(dash.settings(), { ...defaultSettings, ...expected });
}

export async function reopenSettings(page) {
  await page.click("#settings-cancel");
  await page.waitFor(() => !document.getElementById("settings-dialog").open, { description: "settings dialog to close" });
  await openSettings(page);
}

export async function reload(page, dash) {
  await page.goto(`${dash.url}/`);
  await page.waitFor(() => !!document.getElementById("settings-network"), { description: "dashboard to reload" });
}

export async function login(page, dash) {
  await page.goto(`${dash.url}/`);
  await page.type("input[name=password]", dash.password);
  await page.click("button[type=submit]");
  await page.waitFor(() => !!document.getElementById("settings-network") && !!document.getElementById("settings-open"), { description: "dashboard to load after sign-in" });
}

export async function openSettings(page) {
  await page.click("#settings-open");
  await page.waitFor(() => document.getElementById("settings-dialog").open && document.getElementById("settings-status").textContent === "" && !document.getElementById("settings-save").disabled, { description: "settings dialog to finish loading" });
}

export const openAlertPreferences = (page) => page.openDetails("#settings-alert-preferences");

export async function openHostSettings(page) {
  await openSettings(page);
  await page.waitVisible("#settings-network");
}

export async function reopenHostSettings(page) {
  await page.click("#settings-cancel");
  await page.waitFor(() => !document.getElementById("settings-dialog").open, { description: "settings dialog to close" });
  await openHostSettings(page);
}

export const waitSaved = (page) => page.waitText("#settings-status", "Settings saved.", { timeout: 40000 });

export async function waitForFile(dash, name, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const value = dash.readJSON(name);
    if (value !== undefined) return value;
    await sleep(10);
  }
  throw new Error(`timed out waiting for ${name}`);
}

export const withoutIdentity = ({ id, requested_at: requestedAt, ...rest }) => rest;

export async function save(page, expected) {
  await page.click("#settings-save");
  if (expected !== undefined) await page.waitText("#settings-status", expected);
}

export function flow(name, options, body) {
  if (typeof options === "function") [options, body] = [{}, options];
  test(name, { skip: SKIP, timeout: 180000 }, async () => {
    const dash = await startDashboard(options);
    let page;
    try {
      page = await (await sharedChrome()).newPage();
      await login(page, dash);
      await body({ dash, page, helper: dash.helper });
      page.assertClean();
    } finally {
      await page?.close();
      await dash.stop();
    }
  });
}
