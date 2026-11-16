import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { balanceIndexPayload, balanceIndexChange, balanceIndexControl, seedCommand, UTXO_INDEX_OFF_PROMPT, hostControlModel, hostActionPayload } from "./settings.js";

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

test("the balance index switch reverts on a cancelled confirm and hides or disables with the host state", () => {
  assert.deepEqual(balanceIndexChange(false, () => false), { payload: undefined, checked: true });
  assert.deepEqual(balanceIndexChange(false, () => true), { payload: { action: "utxo_index", enabled: false }, checked: false });
  assert.deepEqual(balanceIndexChange(true, () => false), { payload: { action: "utxo_index", enabled: true }, checked: true });
  const bitcoin = { available: true, config_pending: false, utxo_index: true, nodes: { bitcoin: { installed: true } } };
  assert.deepEqual(balanceIndexControl(bitcoin, false), { hidden: false, checked: true, disabled: false });
  assert.equal(balanceIndexControl({ ...bitcoin, nodes: { bitcoin: { installed: false } } }, false).hidden, true);
  assert.equal(balanceIndexControl({ ...bitcoin, available: false }, false).hidden, true);
  assert.equal(balanceIndexControl({ ...bitcoin, config_pending: true }, false).disabled, true);
  assert.equal(balanceIndexControl(bitcoin, true).disabled, true);
});
