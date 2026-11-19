import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { deleteConfirmed, deleteControl, deleteImpact, hostActionPayload, hostControlModel, hostQueueSettled, pendingHostNode, savedHostActions } from "./settings.js";

const node = (id, extra = {}) => ({ id, name: id, installed: true, active: false, last_enabled: false, data_dir: `/srv/${id}`, interfaces: [], ...extra });
const model = (nodes, extra = {}) => hostControlModel({ available: true, delete_supported: true, nodes, ...extra });

test("the delete payload carries the node and the typed confirmation, and nothing else", () => {
  assert.deepEqual(hostActionPayload("delete", "zcash", { confirmation: "zcash" }), { action: "delete", node: "zcash", confirmation: "zcash" });
  assert.equal(hostActionPayload("delete", "zcash", { confirmation: "Zcash" }), undefined);
  assert.equal(hostActionPayload("delete", "zcash", { confirmation: "tari" }), undefined);
  assert.equal(hostActionPayload("delete", "zcash", {}), undefined);
  assert.equal(hostActionPayload("delete", "zcash"), undefined);
  assert.equal(hostActionPayload("delete", "nope", { confirmation: "nope" }), undefined);
});

test("typing the node id is the only way to confirm", () => {
  const target = { id: "bitcoincash", name: "Bitcoin Cash" };
  assert.equal(deleteConfirmed(target, "bitcoincash"), true);
  for (const typed of ["", "Bitcoin Cash", "bitcoincash ", "BITCOINCASH", "bitcoin"]) assert.equal(deleteConfirmed(target, typed), false);
  assert.equal(deleteConfirmed(undefined, "bitcoincash"), false);
});

test("Delete is enabled only for an installed, deactivated node that is not the last one", () => {
  const view = model([node("bitcoin", { active: true }), node("litecoin"), node("zcash", { installed: false })]);
  assert.equal(deleteControl(view, view.nodes.litecoin).disabled, false);
  assert.equal(deleteControl(view, view.nodes.litecoin).reason, "");
  assert.match(deleteControl(view, view.nodes.bitcoin).reason, /Deactivate the node first/);
  assert.match(deleteControl(view, view.nodes.zcash).reason, /not installed/);
  const lonely = model([node("bitcoin", { last_enabled: true })]);
  assert.match(deleteControl(lonely, lonely.nodes.bitcoin).reason, /At least one node must stay installed/);
  const unflagged = model([node("bitcoin"), node("tari", { installed: false })]);
  assert.match(deleteControl(unflagged, unflagged.nodes.bitcoin).reason, /At least one node must stay installed/);
});

test("Delete stays disabled while the host is unavailable, unsupported, busy or has a pending config", () => {
  const nodes = [node("bitcoin", { active: true }), node("litecoin")];
  const reason = (extra) => { const view = model(nodes, extra); return deleteControl(view, view.nodes.litecoin).reason; };
  assert.equal(reason({}), "");
  assert.match(reason({ available: false }), /host helper/);
  assert.match(reason({ delete_supported: false }), /host helper/);
  assert.match(reason({ config_pending: true, config_revision: "a".repeat(64) }), /config first/);
  assert.match(reason({ status: { action: "install", state: "pending" } }), /Another host change/);
  const noFolder = model([node("bitcoin", { active: true }), node("litecoin", { data_dir: "" })]);
  assert.match(deleteControl(noFolder, noFolder.nodes.litecoin).reason, /data folder/);
});

test("staged changes keep Delete honest", () => {
  const view = model([node("litecoin"), node("zcash"), node("bitcoin", { active: true })]);
  const move = hostActionPayload("migrate", "litecoin", { data_dir: "/mnt/big/litecoin" });
  assert.match(deleteControl(view, view.nodes.litecoin, { "migrate:litecoin": move }).reason, /staged data move/);
  const stagedLitecoin = { "delete:litecoin": hostActionPayload("delete", "litecoin", { confirmation: "litecoin" }) };
  assert.equal(deleteControl(view, view.nodes.zcash, stagedLitecoin).disabled, false);
  const both = { ...stagedLitecoin, "delete:zcash": hostActionPayload("delete", "zcash", { confirmation: "zcash" }) };
  const onlyBitcoin = model([node("litecoin"), node("zcash"), node("bitcoin")]);
  assert.match(deleteControl(onlyBitcoin, onlyBitcoin.nodes.bitcoin, both).reason, /At least one node must stay installed/);
});

test("the confirmation names the data folder, volumes, credentials and watch-only wallets", () => {
  const text = (id) => deleteImpact(node(id, { name: id[0].toUpperCase() + id.slice(1) })).join("\n");
  assert.match(text("litecoin"), /\/srv\/litecoin/);
  assert.match(text("litecoin"), /utxo_index_litecoin/);
  assert.match(text("litecoin"), /configuration and credentials/);
  assert.match(text("litecoin"), /watch-only wallets.*saved balances and history/);
  assert.match(text("bitcoin"), /utxo_index\)/);
  assert.match(text("bitcoincash"), /utxo_index_bitcoincash/);
  assert.match(text("dogecoin"), /utxo_index_dogecoin/);
  assert.match(text("monero"), /monero_wallets/);
  assert.match(text("tari"), /tari_wallet, tari_wallet_data/);
  assert.doesNotMatch(text("zcash"), /volume/);
});

test("Save sends a staged delete last, and the delete status reaches the queue", () => {
  const view = model([node("bitcoin", { active: true }), node("litecoin")]);
  const remove = hostActionPayload("delete", "litecoin", { confirmation: "litecoin" });
  const move = hostActionPayload("migrate", "bitcoin", { data_dir: "/mnt/big/bitcoin" });
  assert.deepEqual(savedHostActions(view, {}, { "delete:litecoin": remove, "migrate:bitcoin": move }), [move, remove]);
  const deleting = model([node("litecoin")], { status: { action: "delete", node: "litecoin", state: "pending", message: "Deleting…" } });
  assert.equal(pendingHostNode(deleting, "delete"), "litecoin");
  assert.equal(hostQueueSettled(deleting, { action: "delete", node: "litecoin" }), "");
  const failed = model([node("litecoin")], { status: { action: "delete", node: "litecoin", state: "rejected", message: "no" } });
  assert.equal(hostQueueSettled(failed, { action: "delete", node: "litecoin" }), "failed");
});

test("the Node set row offers Delete next to Move data, behind a typed-confirmation dialog", async () => {
  const [source, html] = await Promise.all([readFile(new URL("./settings.js", import.meta.url), "utf8"), readFile(new URL("../index.html", import.meta.url), "utf8")]);
  assert.match(source, /row\.append\(name, path, button, remove\)/);
  assert.match(source, /"Deletes on Save"/);
  assert.match(source, /deleteSubmit\.disabled = !deleteConfirmed\(/);
  assert.match(html, /<dialog id="settings-delete-dialog"/);
  assert.match(html, /id="settings-delete-submit" type="submit" disabled/);
  assert.match(html, /This cannot be undone/);
});
