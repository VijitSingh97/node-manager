import { test } from "node:test";
import assert from "node:assert/strict";
import { moneroRow, moneroTotal, walletImport, keyImagesImport } from "./monero-watch.js";

test("view-only incoming funds never claim to be a verified current balance", () => {
  const row = moneroRow({ name: "Cold", state: "ready", received_atomic: "1250000000000", balance_atomic: "1250000000000", spending_status: "unknown" });
  assert.equal(row.amount, "1.25");
  assert.equal(row.verified, false);
  assert.equal(row.fiatAsset, "monero");
  assert.match(row.status, /Received funds only · import current key images/);
});

test("only ready wallets with complete current key-image coverage show a verified balance", () => {
  const wallet = { state: "ready", received_atomic: "2000000000000", balance_atomic: "1250000000000", spending_status: "verified", key_images: { complete: true } };
  assert.equal(moneroRow(wallet).amount, "1.25");
  assert.equal(moneroRow(wallet).fiatAsset, "monero");
  assert.match(moneroRow(wallet).status, /Balance verified with key images/);
  assert.equal(moneroRow({ ...wallet, key_images: { complete: false } }).verified, false);
  assert.equal(moneroRow({ ...wallet, spending_status: "stale" }).verified, false);
  assert.equal(moneroRow({ ...wallet, balance_atomic: "" }).verified, false);
  const pending = moneroRow({ ...wallet, state: "pending", scan_height: 50, target_height: 100 });
  assert.equal(pending.amount, "");
  assert.equal(pending.stateText, "Pending scan…");
  assert.match(pending.status, /Scanning · 50\.0%/);
});

test("Monero total never mixes incoming-only funds with verified balances", () => {
  const wallet = { state: "ready", balance_atomic: "1250000000000", spending_status: "verified", key_images: { complete: true } };
  assert.deepEqual(moneroTotal([wallet, wallet]), { text: "2.5 XMR", value: "2.500000000000", fiatAsset: "monero" });
  assert.deepEqual(moneroTotal([wallet, { ...wallet, spending_status: "unknown" }]), { text: "Pending…" });
});

test("public example shows its total received, grouped and priced, with the caveat in its tooltip", () => {
  const example = { example: true, name: "Monero General Fund", state: "ready", received_atomic: "137131438327228521", balance_atomic: "8000000000000", spending_status: "verified", key_images: { complete: true }, updated_at: 1794598261 };
  const personal = { state: "ready", balance_atomic: "1250000000000", spending_status: "verified", key_images: { complete: true } };
  const row = moneroRow(example);
  assert.equal(row.amount, "137,131.438327228521");
  assert.equal(row.unit, "XMR");
  assert.equal(row.verified, false);
  assert.equal(row.fiatAsset, "monero");
  assert.match(row.note, /total ever received, not a current balance/);
  assert.match(row.status, /^Total received · Scanned /);
  assert.equal(moneroTotal([example, personal]).text, "1.25 XMR");
  assert.equal(moneroTotal([example]), null);
});

test("snapshot charts preserve single-atomic-unit changes at large balances", () => {
  const history = ["10000000000000000000", "10000000000000000001", "10000000000000000002"].map(amount_atomic => ({ amount_atomic, verified: false }));
  assert.deepEqual(moneroRow({ state: "ready", received_atomic: "1", history }).series, [0, 0.5, 1]);
  assert.deepEqual(moneroRow({ state: "ready", received_atomic: "1", history: [{ amount_atomic: "oops", verified: false }] }).series, []);
});

test("view-only JSON accepts a private view key and rejects spend keys or seeds", () => {
  const value = { address: "4".repeat(95), view_key: "a".repeat(64), restore_height: 10 };
  assert.equal(walletImport(value).restore_height, 10);
  assert.equal(walletImport(value).name, "Monero wallet");
  assert.equal(walletImport({ ...value, name: " Cold " }).name, "Cold");
  assert.throws(() => walletImport({ ...value, spendkey: "b".repeat(64) }));
  assert.throws(() => walletImport({ ...value, seed: "words" }));
  assert.throws(() => walletImport({ ...value, name: 3 }));
  assert.throws(() => walletImport({ ...value, restore_height: -1 }));
});

test("signed key-image uploads accept native RPC exports and reject malformed signatures", () => {
  const signed_key_images = [{ key_image: "a".repeat(64), signature: "b".repeat(128) }];
  assert.deepEqual(keyImagesImport({ result: { offset: 3, signed_key_images } }), { offset: 3, signed_key_images });
  assert.throws(() => keyImagesImport({ signed_key_images: [{ key_image: "a", signature: "b" }] }));
  assert.throws(() => keyImagesImport({ offset: -1, signed_key_images }));
});
