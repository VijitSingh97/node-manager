import test from "node:test";
import assert from "node:assert/strict";
import { tariAccessView, tariDetails, tariDraftAfterSubmit, tariRow, tariWalletImport } from "./tari-watch.js";

test("view-only JSON accepts only a public spend key and private view key", () => {
  const input = { name: "Cold", public_spend_key: "a".repeat(64), view_key: "b".repeat(64), birthday: "2024-02-29" };
  assert.deepEqual(tariWalletImport(input, "2026-10-31"), input);
  assert.throws(() => tariWalletImport({ ...input, private_spend_key: "c".repeat(64) }, "2026-10-31"), /not accepted/);
  assert.throws(() => tariWalletImport({ ...input, seed: "words" }, "2026-10-31"), /not accepted/);
  assert.throws(() => tariWalletImport({ ...input, birthday: "2023-02-29" }, "2026-10-31"), /Birthday/);
  assert.throws(() => tariWalletImport({ ...input, birthday: "2027-01-01" }, "2026-10-31"), /Birthday/);
  assert.throws(() => tariWalletImport({ ...input, birthday: 20240101 }, "2026-10-31"), /Birthday/);
  assert.equal(tariWalletImport({ ...input, birthday: "" }, "2026-10-31").birthday, "");
  assert.equal(tariWalletImport({ public_spend_key: input.public_spend_key, view_key: input.view_key }, "2026-10-31").name, "Tari wallet");
  assert.throws(() => tariWalletImport({ ...input, name: 3 }, "2026-10-31"), /name/);
});

test("pending and failed scans never display an unverified zero balance", () => {
  for (const [state, text] of [["pending", "Pending scan…"], ["waiting", "Waiting for node sync…"], ["error", "Scan failed"]]) {
    const row = tariRow({ state, balance_atomic: "0" });
    assert.equal(row.amount, "");
    assert.equal(row.stateText, text);
    assert.equal(row.fiatAsset, null);
  }
  assert.equal(tariRow({ state: "ok", balance_atomic: "0" }).amount, "0.000000");
  assert.equal(tariRow({ state: "ok", balance_atomic: "1234567000000" }).amount, "1,234,567.000000");
  assert.equal(tariRow({ state: "ok", balance_atomic: "1234567000000" }).fiatAsset, "tari");
  assert.equal(tariRow({ state: "ok" }).stateText, "Balance unavailable");
});

test("confirmed balance components stay separate and only for a confirmed balance", () => {
  const wallet = { state: "ok", balance_atomic: "5000000", available_atomic: "3000000", timelocked_atomic: "2000000", pending_incoming_atomic: "1000000", pending_outgoing_atomic: "500000" };
  assert.deepEqual(tariDetails(wallet), [
    { label: "Available", value: "3.000000 XTM" },
    { label: "Timelocked", value: "2.000000 XTM" },
    { label: "Pending incoming", value: "1.000000 XTM" },
    { label: "Pending outgoing", value: "0.500000 XTM" },
  ]);
  for (const state of ["pending", "waiting", "error", "removing"]) {
    assert.deepEqual(tariDetails({ state, balance_atomic: "5000000", available_atomic: "0", timelocked_atomic: "5000000" }), []);
  }
  assert.deepEqual(tariDetails({ state: "stale", available_atomic: "0" }), []);
  assert.deepEqual(tariDetails({ state: "stale", scan_time: 1, balance_atomic: "0", available_atomic: "0" }), [{ label: "Available", value: "0.000000 XTM" }]);
  assert.deepEqual(tariDetails({ state: "pending", birthday: "2024-01-01" }), [{ label: "Birthday", value: "2024-01-01" }]);
});

test("snapshot charts preserve a single atomic-unit change", () => {
  const series = tariRow({ state: "ok", balance_atomic: "1", history: [{ balance_atomic: "9007199254740993000000" }, { balance_atomic: "9007199254740993000001" }] }).series;
  assert.deepEqual(series, [0, 1]);
  assert.deepEqual(tariRow({ state: "ok", history: [{ balance_atomic: "1" }] }).series, []);
});

test("editing requires trusted configuration access and an active available wallet service", () => {
  assert.equal(tariAccessView({ has_password: true, active: true, available: true }).canEdit, true);
  assert.equal(tariAccessView({ has_password: false, can_configure: true, active: true, available: true }).canEdit, true);
  assert.match(tariAccessView({ has_password: false, active: true, available: true }).status, /trusted LAN/);
  assert.equal(tariAccessView({ has_password: true, active: false, available: true, reason: "Tari is inactive." }).status, "Tari is inactive.");
  assert.equal(tariAccessView({ has_password: true, active: true, available: false, reason: "Wallet process unavailable." }).status, "Wallet process unavailable.");
});

test("submission drafts never retain the private view key", () => {
  const draft = { name: "Cold", public_spend_key: "a".repeat(64), view_key: "b".repeat(64), birthday: "2024-01-01" };
  assert.deepEqual(tariDraftAfterSubmit(draft, false), { ...draft, view_key: "" });
  assert.deepEqual(tariDraftAfterSubmit(draft, true), { name: "", public_spend_key: "", view_key: "", birthday: "" });
});
