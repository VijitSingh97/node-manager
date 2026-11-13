import { test } from "node:test";
import assert from "node:assert/strict";
import {
  balanceLabel,
  editAccess,
  fiatLabel,
  formatBTCAmount,
  genesisNote,
  isBareExtendedPublicKey,
  publicExampleNote,
  scanNote,
  shortKey,
  totalLabel,
  walletKeyForPost,
  walletPost,
  walletView,
  watchHint,
} from "./watch.js";
import { formatPrice } from "./prices.js";

test("balances group integer digits and keep the fraction verbatim", () => {
  assert.equal(formatBTCAmount("248597.59296803"), "248,597.59296803");
  assert.equal(formatBTCAmount("0.3484765"), "0.3484765");
  assert.equal(formatBTCAmount("1234567"), "1,234,567");
  assert.equal(balanceLabel({ state: "ok", btc: "248597.59296803" }), "248,597.59296803 BTC");
  assert.equal(totalLabel({ total_state: "ok", total: "1000.5" }), "1,000.5 BTC");
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

test("an omitted Bitcoin label gets the first unused wallet name", () => {
  assert.deepEqual(walletPost("", "bc1qaddress", "legacy", "", [
    { name: "Wallet 1" }, { name: "Wallet_2" }, { name: "wallet 3" }, { name: "Binance (public)", example: true },
  ]), { name: "Wallet 4", key: "bc1qaddress", birthday: "" });
  assert.equal(walletPost(" Cold ", "bc1qaddress", "legacy", "", []).name, "Cold");
});

test("balanceLabel shows the balance when ready", () => {
  assert.equal(balanceLabel({ state: "ok", btc: "1.5" }), "1.5 BTC");
  assert.equal(balanceLabel({ state: "ok", btc: "0" }), "0 BTC");
});

test("balanceLabel shows pending history while a rescan runs", () => {
  assert.equal(balanceLabel({ state: "pending", btc: null }), "Pending history…");
  assert.equal(balanceLabel({ state: "scanning", btc: null }), "Pending history…");
});

test("pruned balances distinguish a pending UTXO scan from transaction history", () => {
  assert.equal(balanceLabel({ state: "pending", balance_kind: "confirmed" }), "Pending balance…");
  assert.equal(balanceLabel({ state: "scanning", balance_kind: "confirmed" }), "Pending balance…");
  assert.equal(totalLabel({ total_state: "pending", wallets: [{ balance_kind: "confirmed" }] }), "Pending balance…");
  assert.match(watchHint(true), /confirmed funds in the scanned range/);
  assert.match(watchHint(true), /without mempool transactions or full transaction history/);
  assert.match(watchHint(false), /birthday/);
});

test("the scan note is only the scan timestamp", () => {
  const row = { balance_kind: "confirmed", state: "ok", range: { start: 0, end: 999 }, scan_height: 973634, scan_time: "2026-10-29T03:00:00Z" };
  assert.equal(scanNote(row), `Scanned ${new Date(row.scan_time).toLocaleString()}`);
  assert.equal(scanNote({ ...row, state: "stale" }), scanNote(row));
  assert.equal(scanNote({ state: "ok", btc: "1" }), "");
  assert.equal(scanNote({ balance_kind: "confirmed", scan_time: "bad" }), "");
});

test("balanceLabel keeps an accurate zero after rescan without history", () => {
  assert.equal(balanceLabel({ state: "ok", btc: "0", history: [] }), "0 BTC");
});

test("balanceLabel falls back to a dash on error / not-ready", () => {
  assert.equal(balanceLabel({ state: "error", btc: null }), "—");
  assert.equal(balanceLabel({ state: "whatever" }), "—");
});

test("balanceLabel shows the cached value when stale (node unreachable)", () => {
  assert.equal(balanceLabel({ state: "stale", btc: "1.25" }), "1.25 BTC");
});

test("shortKey shows first 4 … last 4, or the whole thing when short", () => {
  assert.equal(shortKey("1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"), "1A1z…vfNa");
  assert.equal(shortKey("bc1qshort"), "bc1qshort"); // <= 12 chars, shown whole
  assert.equal(shortKey("zpub6rFR7y4Q2AijBEqTU"), "zpub…EqTU");
});

test("walletView displays an alias but preserves the delete identifier", () => {
  assert.deepEqual(walletView({ name: "Satoshi", display_name: "Genesis" }), {
    label: "Genesis",
    deleteName: "Satoshi",
  });
  assert.deepEqual(walletView({ name: "Cold storage" }), {
    label: "Cold storage",
    deleteName: "Cold storage",
  });
  assert.deepEqual(walletView({ name: "Binance (public)", display_name: "Binance", example: true }), {
    label: "Binance (public)",
    deleteName: "Binance (public)",
  });
});

test("public example is explicitly separated from personal assets and reserves", () => {
  assert.match(publicExampleNote({ example: true }), /excluded from your totals/);
  assert.match(publicExampleNote({ example: true }), /not Binance's total reserves/);
  assert.equal(publicExampleNote({}), "");
});

test("genesisNote applies only to the genesis public address", () => {
  assert.equal(
    genesisNote({ key: "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa" }),
    "Bitcoin Core balance excludes the unspendable 50 BTC genesis reward",
  );
  assert.equal(genesisNote({ key: "bc1qexample" }), "");
});

test("editAccess allows passwordless trusted-LAN configuration", () => {
  assert.deepEqual(editAccess({ has_password: false }), {
    enabled: false,
    warning: "Open this dashboard from its trusted LAN address to manage watch-only wallets.",
  });
  assert.deepEqual(editAccess({ has_password: false, can_configure: true }), { enabled: true, warning: "" });
  assert.deepEqual(editAccess({ has_password: true }), { enabled: true, warning: "" });
});

test("bare xpub script choice is explicit and defaults to legacy", () => {
  const xpub = `xpub${"A".repeat(107)}`;
  const tpub = `tpub${"B".repeat(107)}`;
  assert.equal(isBareExtendedPublicKey(xpub), true);
  assert.equal(isBareExtendedPublicKey(tpub), true);
  assert.equal(walletKeyForPost(xpub, "legacy"), xpub);
  assert.equal(walletKeyForPost(xpub, "native"), `wpkh(${xpub}/<0;1>/*)`);
  assert.equal(walletKeyForPost(tpub, "nested"), `sh(wpkh(${tpub}/<0;1>/*))`);
});

test("script choice never rewrites non-bare or private keys", () => {
  const descriptor = "wpkh(xpub661/example/*)";
  const zpub = "zpub6rFR7y4Q2AijBEqTU";
  const xprv = "xprv9s21ZrQH143Kprivate";
  assert.equal(walletKeyForPost(descriptor, "native"), descriptor);
  assert.equal(walletKeyForPost(zpub, "nested"), zpub);
  assert.equal(walletKeyForPost(xprv, "native"), xprv);
  assert.equal(walletKeyForPost("1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "native"),
    "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa");
});

test("totalLabel does not show a partial balance while wallets are unresolved", () => {
  assert.equal(totalLabel({ total_state: "pending", total: "0" }), "Pending history…");
  assert.equal(totalLabel({ total_state: "unavailable", total: "1.5" }), "—");
  assert.equal(totalLabel({ total_state: "ok", total: "1.5" }), "1.5 BTC");
  assert.equal(totalLabel({ total: "2" }), "2 BTC");
});
