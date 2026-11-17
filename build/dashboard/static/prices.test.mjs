import { test, mock } from "node:test";
import assert from "node:assert/strict";
import {
  PRICE_URL,
  formatPrice,
  nextPollDelay,
  normalizeCurrency,
  panelCurrency,
  priceURL,
  priceView,
  scaledPrice,
  towerPriceView,
  watchPrice,
} from "./prices.js";

test("price endpoint is same-origin", () => {
  assert.equal(PRICE_URL, "/api/prices");
  assert.equal(PRICE_URL.startsWith("http"), false);
  assert.equal(priceURL(), "/api/prices");
  assert.equal(priceURL("monero"), "/api/prices?asset=monero");
});

test("configured currency controls a tower when it opens", () => {
  assert.equal(normalizeCurrency("inr"), "INR");
  assert.equal(normalizeCurrency("DOGE"), "USD");
  assert.equal(panelCurrency("bitcoin", "bitcoin", "EUR", "CAD"), "CAD");
  assert.equal(panelCurrency("monero", "bitcoin", "EUR", "CAD"), "EUR");
});

test("rubles and yuan use cached quotes", () => {
  const data = { available: true, stale: false, updated_at: 1700000000,
    prices: { rub: 6500000, cny: 500000 } };
  for (const currency of ["RUB", "CNY"]) {
    const view = priceView(data, currency, () => "now");
    assert.equal(view.currency, currency);
    assert.notEqual(view.value, "—");
    assert.equal(view.status, "As of now");
  }
});

test("formatPrice uses guarded currency formatting", () => {
  const FakeNumberFormat = class {
    constructor(_locale, options) { this.currency = options.currency; }
    format(value) { return `${this.currency}:${value.toFixed(2)}`; }
  };
  assert.equal(formatPrice(57690.48146, "CAD", FakeNumberFormat), "CAD:57690.48");
  const BrokenNumberFormat = class { constructor() { throw new Error("no Intl"); } };
  assert.equal(formatPrice(12.5, "USD", BrokenNumberFormat), "USD 12.50");
});

test("priceView renders current and stale cached prices", () => {
  const current = priceView({
    available: true,
    stale: false,
    updated_at: 1_700_000_000,
    prices: { usd: 57690.48146 },
  }, "USD", () => "Nov 14, 2023, 4:13 PM");
  assert.notEqual(current.value, "—");
  assert.equal(current.status, "As of Nov 14, 2023, 4:13 PM");

  const stale = priceView({
    available: true,
    stale: true,
    updated_at: 1_700_000_000,
    prices: { eur: 51000 },
  }, "EUR", () => "yesterday");
  assert.equal(stale.status, "Stale · as of yesterday");
  assert.match(towerPriceView({
    available: true,
    stale: true,
    updated_at: 1_700_000_000,
    prices: { eur: 51000 },
  }, "EUR").text, / · stale$/);
});

test("priceView never renders zero for unavailable or bad data", () => {
  for (const data of [
    { available: false, prices: { usd: 0 } },
    { available: true, prices: { usd: 0 } },
    { available: true, prices: {} },
    null,
  ]) {
    assert.deepEqual(priceView(data, "USD"), {
      currency: "USD",
      quantity: "",
      value: "—",
      status: "Price unavailable",
      stale: false,
    });
  }
  assert.deepEqual(towerPriceView({ available: false }, "USD"), {
    text: "Price unavailable",
    status: "Price unavailable",
    stale: false,
  });
});

test("scaledPrice picks the smallest power of ten worth at least one unit of currency", () => {
  const close = (actual, quantity, amount) => {
    assert.equal(actual.quantity, quantity);
    assert.ok(Math.abs(actual.amount - amount) < 1e-9, `${actual.amount} != ${amount}`);
  };
  close(scaledPrice(0.0008032), 10000, 8.032);
  close(scaledPrice(0.15), 10, 1.5);
  close(scaledPrice(1), 1, 1);
  close(scaledPrice(57690.48), 1, 57690.48);
  close(scaledPrice(1.01), 1, 1.01);
  close(scaledPrice(0.999), 10, 9.99);
  close(scaledPrice(0.001), 1000, 1);
  close(scaledPrice(1e-7), 10000000, 1);
  close(scaledPrice(1e-11), 100000000000, 1);
  close(scaledPrice(3e-9), 1000000000, 3);
  close(scaledPrice("0.0008032"), 10000, 8.032);
  for (const bad of [0, -0.5, null, undefined, "", "abc", NaN, Infinity]) {
    assert.equal(scaledPrice(bad), null, String(bad));
  }
});

test("priceView labels a scaled quantity and keeps whole-unit prices plain", () => {
  const data = (usd) => ({ available: true, stale: false, updated_at: 1_700_000_000, prices: { usd } });
  const tari = priceView(data(0.0008032), "USD", () => "now");
  assert.equal(tari.quantity, (10000).toLocaleString());
  assert.equal(tari.value, formatPrice(8.032, "USD"));
  assert.equal(tari.status, "As of now");
  assert.equal(priceView(data(0.15), "USD").quantity, (10).toLocaleString());
  assert.equal(priceView(data(1), "USD").quantity, "");
  assert.equal(priceView(data(57690.48), "USD").value, formatPrice(57690.48, "USD"));
  assert.equal(priceView(data(57690.48), "USD").quantity, "");
  assert.equal(towerPriceView(data(0.0008032), "USD").text, `${formatPrice(8.032, "USD")} per ${(10000).toLocaleString()}`);
  assert.equal(towerPriceView(data(2), "USD").text, formatPrice(2, "USD"));
  assert.equal(towerPriceView({ ...data(0.15), stale: true }, "USD").text,
    `${formatPrice(1.5, "USD")} per ${(10).toLocaleString()} · stale`);
});

test("priceView rescales from the selected currency's quote", () => {
  const data = { available: true, stale: false, updated_at: 1_700_000_000, prices: { usd: 0.0008032, jpy: 0.12 } };
  assert.equal(priceView(data, "USD").quantity, (10000).toLocaleString());
  assert.equal(priceView(data, "JPY").quantity, (10).toLocaleString());
});

test("the page polls quickly until prices are fresh", () => {
  assert.equal(nextPollDelay({ available: true, stale: false }), 60000);
  assert.equal(nextPollDelay({ available: true, stale: true }), 10000);
  assert.equal(nextPollDelay({ available: false }), 10000);
  assert.equal(nextPollDelay(undefined), 10000);
});

async function settle() {
  for (let i = 0; i < 5; i++) await Promise.resolve();
}

async function watchWith(asset, bodies, listener) {
  const counter = { calls: 0 };
  globalThis.fetch = async () => {
    const body = bodies[Math.min(counter.calls++, bodies.length - 1)];
    return { ok: body !== null, json: async () => body };
  };
  mock.timers.enable({ apis: ["setTimeout"] });
  watchPrice(asset, listener && (() => listener(counter)));
  await settle();
  return counter;
}

test("watchPrice retries in 10 s after a failed fetch and settles to 60 s once fresh", async (t) => {
  t.after(() => mock.timers.reset());
  const counter = await watchWith("retry-asset", [null, { available: true, stale: true }, { available: true }, { available: true }]);
  const step = async (ms, expected) => {
    mock.timers.tick(ms);
    await settle();
    assert.equal(counter.calls, expected);
  };
  assert.equal(counter.calls, 1);
  await step(9999, 1);
  await step(1, 2);
  await step(10000, 3);
  await step(59999, 3);
  await step(1, 4);
});

test("watchPrice schedules its next poll before notifying listeners", async (t) => {
  t.after(() => mock.timers.reset());
  let polledFromListener = -1;
  await watchWith("order-asset", [{ available: true }], (counter) => {
    if (polledFromListener >= 0) return;
    const before = counter.calls;
    mock.timers.tick(60000);
    polledFromListener = counter.calls - before;
  });
  assert.equal(polledFromListener, 1);
});
