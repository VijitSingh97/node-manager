import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { balanceIndexPayload, confirmedHostChanges, balanceIndexControl, UTXO_INDEX_OFF_PROMPT, UTXO_INDEX_LITECOIN_OFF_PROMPT, UTXO_INDEX_BITCOINCASH_OFF_PROMPT, UTXO_INDEX_DOGECOIN_OFF_PROMPT, hostControlModel, hostActionPayload, hostToggleChanges, hostQueueSettled, hostQueueSummary, hostChangeLabel, splitDependentChanges } from "./settings.js";

test("the balance index switch reads its state, builds a boolean payload and routes its status", () => {
  assert.equal(hostControlModel({}).utxo_index, false);
  assert.equal(hostControlModel({ utxo_index: true }).utxo_index, true);
  assert.equal(hostControlModel({ utxo_index: "true" }).utxo_index, false);
  assert.equal(hostControlModel({ status: { action: "utxo_index", state: "applied", message: "ok" } }).status.action, "utxo_index");
  assert.deepEqual(hostActionPayload("utxo_index", true), { action: "utxo_index", enabled: true });
  assert.equal(hostActionPayload("utxo_index", "yes"), undefined);
});

test("turning the balance index on needs no confirmation and turning it off asks first", async () => {
  const asked = [];
  assert.deepEqual(await balanceIndexPayload(true, (m) => { asked.push(m); return false; }), { action: "utxo_index", enabled: true });
  assert.deepEqual(asked, []);
  assert.equal(await balanceIndexPayload(false, (m) => { asked.push(m); return false; }), undefined);
  assert.deepEqual(asked, [UTXO_INDEX_OFF_PROMPT()]);
  assert.deepEqual(await balanceIndexPayload(false, () => true), { action: "utxo_index", enabled: false });
  assert.match(UTXO_INDEX_OFF_PROMPT(), /utxo-index remove/);
});

test("the settings page carries none of the removed explanatory notes", async () => {
  const sources = await Promise.all(["./settings.js", "../testdata/index.rendered.html"].map((file) => readFile(new URL(file, import.meta.url), "utf8")));
  for (const source of sources) {
    for (const removed of ["For uptime monitors and scripts", "For pruned nodes", "needs 27 GB free", "All traffic stays on Tor", "utxo-seed-cli", "utxo-seed-command"]) {
      assert.equal(source.includes(removed), false, removed);
    }
  }
});

test("saving the balance index off drops only that change on a cancelled confirm, and the switch hides or disables with the host state", async () => {
  const off = { action: "utxo_index", enabled: false };
  const boot = { action: "launch_at_boot", enabled: true };
  assert.deepEqual(await confirmedHostChanges([boot, off], () => false), [boot]);
  assert.deepEqual(await confirmedHostChanges([boot, off], () => true), [boot, off]);
  assert.deepEqual(await confirmedHostChanges([{ action: "utxo_index", enabled: true }], () => false), [{ action: "utxo_index", enabled: true }]);
  const bitcoin = { available: true, config_pending: false, utxo_index: true, nodes: { bitcoin: { installed: true } } };
  assert.deepEqual(balanceIndexControl(bitcoin, false), { hidden: false, checked: true, disabled: false });
  assert.equal(balanceIndexControl({ ...bitcoin, nodes: { bitcoin: { installed: false } } }, false).hidden, true);
  assert.equal(balanceIndexControl({ ...bitcoin, available: false }, false).hidden, true);
  assert.equal(balanceIndexControl({ ...bitcoin, config_pending: true }, false).disabled, true);
  assert.equal(balanceIndexControl(bitcoin, true).disabled, true);
});

test("the seed buttons and their notes are gone, and a seed status still routes to the balance line", async () => {
  const sources = await Promise.all(["./settings.js", "../testdata/index.rendered.html"].map((file) => readFile(new URL(file, import.meta.url), "utf8")));
  for (const source of sources) {
    for (const removed of ["settings-utxo-seed-now", "utxo_seed_now", "Seed Litecoin now", "Replay Bitcoin Cash now", "Seed Dogecoin now", "bitcoincash-note", "dogecoin-note", "archival node)", "has no coin-set export"]) {
      assert.equal(source.includes(removed), false, removed);
    }
  }
  assert.equal(hostControlModel({ utxo_seed_now: true }).utxo_seed_now, undefined);
  assert.equal(hostControlModel({ status: { action: "utxo_seed", state: "applied", message: "Balance index seed started." } }).status.action, "utxo_seed");
});

test("the install request carries the balance index option only when the node can index", () => {
  const options = { data_dir: "/data/doge", interfaces: [] };
  assert.deepEqual(hostActionPayload("install", "dogecoin", { ...options, utxo_index: true }), { action: "install", node: "dogecoin", data_dir: "/data/doge", interfaces: [], utxo_index: true });
  assert.equal(hostActionPayload("install", "dogecoin", { ...options, utxo_index: false }).utxo_index, false);
  assert.equal("utxo_index" in hostActionPayload("install", "dogecoin", options), false);
  assert.equal("utxo_index" in hostActionPayload("install", "dogecoin", { ...options, utxo_index: "yes" }), false);
  assert.deepEqual(hostActionPayload("install", "dogecoin"), { action: "install", node: "dogecoin" });
  const model = hostControlModel({ install_index_supported: true, nodes: [{ id: "bitcoincash", name: "Bitcoin Cash", can_index: true, index_refusal: "The balance index requires Bitcoin Cash to be enabled.", interfaces: [] }, { id: "monero", name: "Monero", interfaces: [] }] });
  assert.equal(model.install_index_supported, true);
  assert.equal(model.nodes.bitcoincash.can_index, true);
  assert.equal(model.nodes.bitcoincash.index_refusal, "The balance index requires Bitcoin Cash to be enabled.");
  assert.equal(model.nodes.monero.can_index, false);
  assert.equal(hostControlModel({}).install_index_supported, false);
});

test("the Litecoin balance index switch stages its own node-scoped payload", async () => {
  const view = hostControlModel({ utxo_index: true, utxo_index_litecoin: false });
  assert.equal(view.utxo_index_litecoin, false);
  assert.deepEqual(hostActionPayload("utxo_index_litecoin", true), { action: "utxo_index", node: "litecoin", enabled: true });
  assert.deepEqual(hostToggleChanges(view, { utxo_index_litecoin: true }), [{ action: "utxo_index", node: "litecoin", enabled: true }]);
  const off = { action: "utxo_index", node: "litecoin", enabled: false };
  const asked = [];
  assert.deepEqual(await confirmedHostChanges([off], (m) => { asked.push(m); return false; }), []);
  assert.deepEqual(asked, [UTXO_INDEX_LITECOIN_OFF_PROMPT()]);
  assert.deepEqual(await confirmedHostChanges([off], () => true), [off]);
  assert.equal(hostControlModel({ status: { action: "utxo_index", node: "litecoin", state: "applied", message: "ok" } }).status.node, "litecoin");
});

test("the Bitcoin Cash balance index switch stages its own payload and asks before turning off", async () => {
  const view = hostControlModel({ utxo_index_bitcoincash: false });
  assert.deepEqual(hostActionPayload("utxo_index_bitcoincash", true), { action: "utxo_index", node: "bitcoincash", enabled: true });
  assert.deepEqual(hostToggleChanges(view, { utxo_index_bitcoincash: true }), [{ action: "utxo_index", node: "bitcoincash", enabled: true }]);
  assert.equal(hostActionPayload("utxo_index_zcash", true), undefined);
  const off = { action: "utxo_index", node: "bitcoincash", enabled: false };
  const asked = [];
  assert.deepEqual(await confirmedHostChanges([off], (m) => { asked.push(m); return false; }), []);
  assert.deepEqual(asked, [UTXO_INDEX_BITCOINCASH_OFF_PROMPT()]);
  assert.match(UTXO_INDEX_BITCOINCASH_OFF_PROMPT(), /utxo-index remove bitcoincash/);
});

test("the Dogecoin balance index switch stages its own payload and asks before turning off", async () => {
  const view = hostControlModel({ utxo_index_dogecoin: false });
  assert.equal(view.utxo_index_dogecoin, false);
  assert.deepEqual(hostActionPayload("utxo_index_dogecoin", true), { action: "utxo_index", node: "dogecoin", enabled: true });
  assert.deepEqual(hostToggleChanges(view, { utxo_index_dogecoin: true }), [{ action: "utxo_index", node: "dogecoin", enabled: true }]);
  const off = { action: "utxo_index", node: "dogecoin", enabled: false };
  const asked = [];
  assert.deepEqual(await confirmedHostChanges([off], (m) => { asked.push(m); return false; }), []);
  assert.deepEqual(asked, [UTXO_INDEX_DOGECOIN_OFF_PROMPT()]);
  assert.match(UTXO_INDEX_DOGECOIN_OFF_PROMPT(), /utxo-index remove dogecoin/);
});

test("a failed host change is matched by action, node and id, and only holds back changes to the same node", () => {
  const failedFor = (node, id) => ({ status: { action: "utxo_index", node, id, state: "rejected", message: "no" } });
  const bch = { action: "utxo_index", node: "bitcoincash", enabled: true, requestId: "a".repeat(32) };
  assert.equal(hostQueueSettled(failedFor("bitcoincash", "a".repeat(32)), bch), "failed");
  assert.equal(hostQueueSettled(failedFor("dogecoin", "a".repeat(32)), bch), "applied");
  assert.equal(hostQueueSettled(failedFor("bitcoincash", "b".repeat(32)), bch), "applied");
  assert.equal(hostQueueSettled({ status: { action: "utxo_index", node: "bitcoincash", state: "pending" } }, bch), "");
  const doge = { action: "utxo_index", node: "dogecoin", enabled: true };
  const dogeInstall = { action: "install", node: "bitcoincash", data_dir: "/x", interfaces: [] };
  const boot = { action: "launch_at_boot", enabled: true };
  const { rest, skipped } = splitDependentChanges([doge, dogeInstall, boot], bch);
  assert.deepEqual(rest, [doge, boot]);
  assert.deepEqual(skipped, [dogeInstall]);
  assert.deepEqual(splitDependentChanges([doge, boot], { action: "utxo_index", enabled: true }).skipped, []);
  const btcToggle = { action: "utxo_index", enabled: true };
  assert.deepEqual(splitDependentChanges([btcToggle, boot], { action: "install", node: "bitcoin", data_dir: "/x", interfaces: [] }).skipped, [btcToggle]);
});

test("the queue summary names every failed change with the host's real message", () => {
  const nodes = { bitcoincash: { name: "Bitcoin Cash" }, dogecoin: { name: "Dogecoin" } };
  assert.equal(hostQueueSummary([], []), "Settings saved.");
  assert.equal(hostChangeLabel({ action: "utxo_index", node: "bitcoincash" }, nodes), "Bitcoin Cash balance index");
  assert.equal(hostChangeLabel({ action: "utxo_index" }, nodes), "Bitcoin balance index");
  assert.equal(hostChangeLabel({ action: "install", node: "dogecoin" }, nodes), "Install Dogecoin");
  const message = "The requested host change did not pass validation. Balance index setting requires Bitcoin Cash to be enabled.";
  assert.equal(hostQueueSummary([{ label: "Bitcoin Cash balance index", message }], []), `Bitcoin Cash balance index: ${message} Skipped: none.`);
  assert.match(hostQueueSummary([{ label: "A", message: "x." }, { label: "B", message: "y." }], ["C", "D"]), /^A: x\. B: y\. Skipped: C, D\.$/);
});

test("the install dialog offers a balance index checkbox ticked by default, next to the data folder", async () => {
  const [html, source] = await Promise.all(["../testdata/index.rendered.html", "./settings.js"].map((file) => readFile(new URL(file, import.meta.url), "utf8")));
  assert.match(html, /<label id="settings-install-index-label"[^>]*hidden><input id="settings-install-index" type="checkbox" checked>/);
  assert.ok(html.indexOf("settings-install-data-dir") < html.indexOf("settings-install-index-label") && html.indexOf("settings-install-index-label") < html.indexOf("settings-install-interfaces"));
  assert.match(source, /node\.can_index && hostView\.install_index_supported/);
  assert.match(source, /installIndex\.checked = true/);
  assert.match(source, /refusal\) input\.disabled = true/);
});

test("removal commands and the launch label follow the install type", () => {
  assert.match(UTXO_INDEX_OFF_PROMPT(), /run \.\/stack utxo-index remove\.$/);
  for (const command of ["sudo node-manager", "node-manager"]) {
    globalThis.document = { body: { dataset: { command } } };
    try {
      assert.ok(UTXO_INDEX_OFF_PROMPT().endsWith(`run ${command} utxo-index remove.`), command);
    } finally {
      delete globalThis.document;
    }
  }
  assert.equal(hostChangeLabel({ action: "launch_at_boot" }, {}, "login"), "Launch at login");
  assert.equal(hostChangeLabel({ action: "launch_at_boot" }, {}, "boot"), "Launch at boot");
});
