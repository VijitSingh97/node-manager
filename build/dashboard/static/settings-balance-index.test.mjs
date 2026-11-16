import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { balanceIndexPayload, confirmedHostChanges, balanceIndexControl, seedCommand, UTXO_INDEX_OFF_PROMPT, UTXO_INDEX_LITECOIN_OFF_PROMPT, hostControlModel, hostActionPayload, hostToggleChanges } from "./settings.js";

test("the balance index switch reads its state, builds a boolean payload and routes its status", () => {
  assert.equal(hostControlModel({}).utxo_index, false);
  assert.equal(hostControlModel({ utxo_index: true }).utxo_index, true);
  assert.equal(hostControlModel({ utxo_index: "true" }).utxo_index, false);
  assert.equal(hostControlModel({ status: { action: "utxo_index", state: "applied", message: "ok" } }).status.action, "utxo_index");
  assert.deepEqual(hostActionPayload("utxo_index", true), { action: "utxo_index", enabled: true });
  assert.equal(hostActionPayload("utxo_index", "yes"), undefined);
  assert.equal(seedCommand("boot"), "sudo node-manager utxo-seed");
  assert.equal(seedCommand("login"), "node-manager utxo-seed");
  assert.equal(seedCommand("unsupported"), "./stack utxo-seed");
  assert.equal(seedCommand(undefined), "./stack utxo-seed");
});

test("turning the balance index on needs no confirmation and turning it off asks first", () => {
  const asked = [];
  assert.deepEqual(balanceIndexPayload(true, (m) => { asked.push(m); return false; }), { action: "utxo_index", enabled: true });
  assert.deepEqual(asked, []);
  assert.equal(balanceIndexPayload(false, (m) => { asked.push(m); return false; }), undefined);
  assert.deepEqual(asked, [UTXO_INDEX_OFF_PROMPT]);
  assert.deepEqual(balanceIndexPayload(false, () => true), { action: "utxo_index", enabled: false });
  assert.match(UTXO_INDEX_OFF_PROMPT, /utxo-index remove/);
});

test("the balance index note states the free-space and RPC pause requirements", async () => {
  const source = await readFile(new URL("./settings.js", import.meta.url), "utf8");
  assert.match(source, /needs 27 GB free/);
  assert.match(source, /Bitcoin RPC pauses for about 5 minutes/);
});

test("saving the balance index off drops only that change on a cancelled confirm, and the switch hides or disables with the host state", () => {
  const off = { action: "utxo_index", enabled: false };
  const boot = { action: "launch_at_boot", enabled: true };
  assert.deepEqual(confirmedHostChanges([boot, off], () => false), [boot]);
  assert.deepEqual(confirmedHostChanges([boot, off], () => true), [boot, off]);
  assert.deepEqual(confirmedHostChanges([{ action: "utxo_index", enabled: true }], () => false), [{ action: "utxo_index", enabled: true }]);
  const bitcoin = { available: true, config_pending: false, utxo_index: true, nodes: { bitcoin: { installed: true } } };
  assert.deepEqual(balanceIndexControl(bitcoin, false), { hidden: false, checked: true, disabled: false });
  assert.equal(balanceIndexControl({ ...bitcoin, nodes: { bitcoin: { installed: false } } }, false).hidden, true);
  assert.equal(balanceIndexControl({ ...bitcoin, available: false }, false).hidden, true);
  assert.equal(balanceIndexControl({ ...bitcoin, config_pending: true }, false).disabled, true);
  assert.equal(balanceIndexControl(bitcoin, true).disabled, true);
});

test("Seed now follows the host view and routes its status to the balance index", () => {
  assert.equal(hostControlModel({}).utxo_seed_now, false);
  assert.equal(hostControlModel({ utxo_seed_now: true }).utxo_seed_now, true);
  assert.equal(hostControlModel({ utxo_seed_now: "true" }).utxo_seed_now, false);
  assert.equal(hostControlModel({ status: { action: "utxo_seed", state: "applied", message: "Balance index seed started." } }).status.action, "utxo_seed");
});

test("the Litecoin balance index switch stages its own node-scoped payload", () => {
  const view = hostControlModel({ utxo_index: true, utxo_index_litecoin: false, utxo_seed_now_litecoin: true });
  assert.equal(view.utxo_index_litecoin, false);
  assert.equal(view.utxo_seed_now_litecoin, true);
  assert.deepEqual(hostActionPayload("utxo_index_litecoin", true), { action: "utxo_index", node: "litecoin", enabled: true });
  assert.deepEqual(hostToggleChanges(view, { utxo_index_litecoin: true }), [{ action: "utxo_index", node: "litecoin", enabled: true }]);
  const off = { action: "utxo_index", node: "litecoin", enabled: false };
  const asked = [];
  assert.deepEqual(confirmedHostChanges([off], (m) => { asked.push(m); return false; }), []);
  assert.deepEqual(asked, [UTXO_INDEX_LITECOIN_OFF_PROMPT]);
  assert.deepEqual(confirmedHostChanges([off], () => true), [off]);
  assert.equal(hostControlModel({ status: { action: "utxo_index", node: "litecoin", state: "applied", message: "ok" } }).status.node, "litecoin");
});
