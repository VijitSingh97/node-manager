import test from "node:test";
import assert from "node:assert/strict";
import { familyAccessView, familyAmount, familyRow, familySourceURL, familyWalletPost } from "./family-watch.js";

test("Litecoin and Bitcoin Cash labels are optional", () => {
  assert.deepEqual(familyWalletPost("litecoin", "", " Laddress "), { label: "Litecoin wallet", address: "Laddress" });
  assert.deepEqual(familyWalletPost("bitcoincash", " Savings ", " qaddress "), { label: "Savings", address: "qaddress" });
});

test("family amounts preserve the backend decimal string including verified zero", () => {
  assert.equal(familyAmount("0.00000000"), "0.00000000");
  assert.equal(familyAmount("9007199254740993.12345678"), "9007199254740993.12345678");
  for (const value of [0, "1", "1.2", "01.00000000", "1.000000000"]) assert.equal(familyAmount(value), "");
});

test("wallet states never present pending or failed scans as zero", () => {
  for (const [state, text] of [["pending", "Scanning for confirmed funds…"], ["waiting", "Waiting for node sync…"], ["error", "Scan failed"]]) {
    const row = familyRow({ state, amount: null }, "litecoin");
    assert.equal(row.amount, "");
    assert.equal(row.stateText, text);
    assert.equal(row.fiatAsset, null);
  }
  const zero = familyRow({ state: "ok", amount: "0.00000000" }, "litecoin");
  assert.equal(`${zero.amount} ${zero.unit}`, "0.00000000 LTC");
  assert.equal(zero.fiatAsset, "litecoin");
});

test("stale balances remain visible, grouped, and marked as cached", () => {
  const row = familyRow({ state: "stale", amount: "1234.34567890", scan_time: "2026-10-31T12:00:00Z" }, "bitcoincash");
  assert.equal(`${row.amount} ${row.unit}`, "1,234.34567890 BCH");
  assert.equal(row.fiatAsset, "bitcoin-cash");
  assert.equal(row.stale, true);
  assert.match(row.status, /^Cached balance; a fresh scan is unavailable · Scanned /);
});

test("public examples carry their caveat in the tooltip", () => {
  const row = familyRow({ label: "Linux Mint", address: "Lexample", example: true, state: "ok", amount: "1.00000000" }, "litecoin");
  assert.equal(row.label, "Linux Mint");
  assert.match(row.note, /Public example; excluded from your assets/);
});

test("editing requires a supported active node and trusted configuration access", () => {
  assert.equal(familyAccessView({ supported: true, enabled: true, active: true, has_password: true }, "litecoin").canEdit, true);
  assert.equal(familyAccessView({ supported: true, enabled: true, active: true, has_password: false, can_configure: true }, "litecoin").canEdit, true);
  assert.match(familyAccessView({ supported: true, enabled: true, active: true, has_password: false }, "litecoin").status, /trusted LAN/);
  assert.equal(familyAccessView({ supported: false, reason: "Dogecoin scanner details." }, "dogecoin").status, "Wallet balance not available.");
  assert.equal(familyAccessView({ supported: false, reason: "Zcash scanner details." }, "zcash").status, "Wallet balance not available.");
  assert.equal(familyAccessView({ supported: false, reason: "Tari wallet service unavailable." }, "tari").status, "Tari wallet service unavailable.");
  assert.equal(familyAccessView({ supported: true, enabled: true, active: false, reason: "Litecoin is inactive." }, "litecoin").status, "Litecoin is inactive.");
});

test("source links accept HTTPS only", () => {
  assert.equal(familySourceURL("http://example.com"), "");
  assert.equal(familySourceURL("javascript:alert(1)"), "");
  assert.equal(familySourceURL("https://www.linuxmint.com/donors.php"), "https://www.linuxmint.com/donors.php");
});

test("native Unix scan timestamps read the same as RFC 3339 ones", () => {
  const seconds = Date.parse("2026-10-31T12:00:00Z") / 1000;
  assert.equal(familyRow({ state: "ok", amount: "1.00000000", scan_time: seconds }, "litecoin").status, familyRow({ state: "ok", amount: "1.00000000", scan_time: "2026-10-31T12:00:00Z" }, "litecoin").status);
});

test("saved balance charts preserve a single atomic-unit change", () => {
  const series = familyRow({ state: "ok", amount: "1.00000000", history: [{ amount: "9007199254740993.00000000" }, { amount: "9007199254740993.00000001" }] }, "litecoin").series;
  assert.deepEqual(series, [0, 1]);
  assert.deepEqual(familyRow({ state: "ok", amount: "1.00000000", history: [{ amount: "0.00000000" }] }, "litecoin").series, []);
});
