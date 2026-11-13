import { test } from "node:test";
import assert from "node:assert/strict";
import { atomicToDecimal, bigSeries, fiatLabel, formatAmount, groupDigits, httpsURL, joinNote, scanNote, shortKey } from "./wallet-format.js";
import { rowView } from "./wallet-ui.js";
import { formatPrice } from "./prices.js";

test("amounts group integer digits and keep or trim the fraction", () => {
  assert.equal(groupDigits("0"), "0");
  assert.equal(groupDigits("1000"), "1,000");
  assert.equal(groupDigits("1234567890"), "1,234,567,890");
  assert.equal(formatAmount("248597.59296803"), "248,597.59296803");
  assert.equal(formatAmount("0.3484765"), "0.3484765");
  assert.equal(formatAmount("1234567"), "1,234,567");
  assert.equal(formatAmount("137131.438327228521", true), "137,131.438327228521");
  assert.equal(formatAmount("10.500000000000", true), "10.5");
  assert.equal(formatAmount("1000.000000000000", true), "1,000");
  assert.equal(formatAmount("0.000000000000", true), "0");
  assert.equal(formatAmount("not a number"), "");
});

test("atomic amounts convert exactly without floating point", () => {
  assert.equal(atomicToDecimal("137131438327228521", 12), "137131.438327228521");
  assert.equal(atomicToDecimal("1", 12), "0.000000000001");
  assert.equal(atomicToDecimal("1250000", 6), "1.250000");
  for (const bad of ["", "-1", "1.5", "abc", 5, null]) assert.equal(atomicToDecimal(bad, 12), null);
});

test("fiat labels follow the price feed and currency", () => {
  const price = { available: true, prices: { usd: 100000, eur: 90000 } };
  assert.equal(fiatLabel("2", price, "USD"), `≈ ${formatPrice(200000, "USD")}`);
  assert.equal(fiatLabel("2", price, "EUR"), `≈ ${formatPrice(180000, "EUR")}`);
  assert.equal(fiatLabel("2", { ...price, stale: true }, "USD"), `≈ ${formatPrice(200000, "USD")} · stale`);
  assert.equal(fiatLabel("2", { available: false }, "USD"), "");
  assert.equal(fiatLabel("2", { available: true, prices: {} }, "USD"), "");
  assert.equal(fiatLabel(null, price, "USD"), "");
});

test("scan times accept RFC 3339 strings and unix seconds alike", () => {
  const seconds = Date.parse("2026-11-13T19:44:31Z") / 1000;
  assert.equal(scanNote("2026-11-13T19:44:31Z"), scanNote(seconds));
  assert.match(scanNote(seconds), /^Scanned /);
  assert.equal(scanNote("bad"), "");
  assert.equal(scanNote(0), "");
  assert.equal(joinNote(["a", "", null, "b"]), "a · b");
});

test("sparkline series keep single-atomic-unit changes", () => {
  assert.deepEqual(bigSeries(["10000000000000000000", "10000000000000000001", "10000000000000000002"]), [0, 0.5, 1]);
  assert.deepEqual(bigSeries(["oops"]), []);
});

test("short keys and safe links", () => {
  assert.equal(shortKey("1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"), "1A1z…vfNa");
  assert.equal(shortKey("bc1qshort"), "bc1qshort");
  assert.equal(httpsURL("https://www.getmonero.org/get-started/contributing/"), "https://www.getmonero.org/get-started/contributing/");
  for (const bad of ["http://example.com", "javascript:alert(1)", "", null]) assert.equal(httpsURL(bad), "");
});

test("every chain's row renders the same way", () => {
  const price = { available: true, prices: { usd: 70000 } };
  const view = rowView({ label: "Binance (public)", note: "Public example", value: "248597.59296803", amount: "248,597.59296803", unit: "BTC", fiatAsset: "bitcoin", status: "Scanned now" }, price, "USD");
  assert.equal(view.name, "Binance (public)\u00a0ⓘ");
  assert.equal(view.nameTitle, "Public example");
  assert.equal(view.balance, "248,597.59296803 BTC");
  assert.equal(view.balanceAria, "Binance (public): 248,597.59296803 BTC");
  assert.match(view.fiat, /^≈ /);
  assert.equal(view.status, "Scanned now");
  const received = rowView({ label: "Monero General Fund", note: "x", value: "137131.438327228521", amount: "137,131.438327228521", unit: "XMR", fiatAsset: null }, price, "USD");
  assert.equal(received.fiat, "");
  const pending = rowView({ label: "New", amount: "", stateText: "Pending scan…", unit: "XMR", stale: true }, price, "USD");
  assert.equal(pending.balance, "Pending scan…");
  assert.equal(pending.stale, true);
  assert.equal(pending.name, "New");
});
