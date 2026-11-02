import { test } from "node:test";
import assert from "node:assert/strict";
import { needsFullReload, replaceLive, replaceUpdateViews } from "./refresh.js";

const bitcoinMounts = ["tower-stats", "watch", "btc-price"];

function page(...ids) {
  const elements = new Set(ids);
  return { getElementById: (id) => elements.has(id) ? {} : null };
}

function classList(...names) {
  const values = new Set(names);
  return {
    contains: (name) => values.has(name),
    toggle(name, enabled) {
      if (enabled) values.add(name);
      else values.delete(name);
    },
    values: () => [...values].sort(),
  };
}

function live(label, price) {
  return {
    id: "live",
    label,
    price,
    querySelector: (selector) => selector === "#btc-price" ? price : null,
  };
}

function documentFixture(label, bodyClasses = [], includeLive = true) {
  const currency = { value: "EUR" };
  const price = { id: "btc-price", currency };
  const doc = {
    body: { classList: classList(...bodyClasses), dataset: { node: "bitcoin", custom: "kept" } },
    canvas: { id: "tower", frame: 17 },
    walletForm: { id: "watch-form", draft: "bc1qkept" },
    settings: { id: "settings-dialog", open: true },
    notices: {
      "update-notice": { id: "update-notice", text: `${label}-overview` },
      "settings-update": { id: "settings-update", text: `${label}-settings` },
    },
    persistent: {
      "tower-stats": { id: "tower-stats" },
      watch: { id: "watch" },
    },
    live: includeLive ? live(label, price) : null,
  };
  if (doc.live) {
    doc.live.replaceWith = (next) => { doc.live = next; };
    price.replaceWith = (next) => { doc.live.price = next; };
  }
  for (const id of Object.keys(doc.notices)) {
    doc.notices[id].replaceWith = (next) => { doc.notices[id] = next; };
  }
  doc.getElementById = (id) => {
    if (id === "live") {
      if (doc.live) doc.live.replaceWith = (next) => { doc.live = next; };
      return doc.live;
    }
    if (id === "btc-price") return doc.live?.price || null;
    if (id === "tower") return doc.canvas;
    if (id === "watch-form") return doc.walletForm;
    if (id === "settings-dialog") return doc.settings;
    return doc.notices[id] || doc.persistent[id] || null;
  };
  return doc;
}

test("Bitcoin loading and ready shells refresh in place", () => {
  const loading = page("live", ...bitcoinMounts);
  const ready = page("live", ...bitcoinMounts);
  assert.equal(needsFullReload(loading, ready), false);
  assert.equal(needsFullReload(ready, loading), false);
});

test("adding or removing the configured Bitcoin shell needs a full reload", () => {
  const disabled = page("live");
  const enabled = page("live", ...bitcoinMounts);
  assert.equal(needsFullReload(disabled, enabled), true);
  assert.equal(needsFullReload(enabled, disabled), true);
});

test("live refresh preserves persistent state through loading and ready transitions", () => {
  const current = documentFixture("loading", ["theme-dark", "center"]);
  const canvas = current.canvas;
  const walletForm = current.walletForm;
  const settings = current.settings;
  const towerStats = current.persistent["tower-stats"];
  const watch = current.persistent.watch;
  const price = current.live.price;
  const currency = price.currency;
  const dataset = current.body.dataset;

  const ready = documentFixture("ready", ["center-screen"]);
  assert.equal(replaceLive(current, ready), true);
  assert.equal(current.live.label, "ready");
  assert.equal(current.getElementById("btc-price"), price);
  assert.equal(current.getElementById("btc-price").currency, currency);
  assert.equal(currency.value, "EUR");
  assert.equal(current.canvas, canvas);
  assert.equal(current.walletForm, walletForm);
  assert.equal(current.settings, settings);
  assert.equal(current.getElementById("tower-stats"), towerStats);
  assert.equal(current.getElementById("watch"), watch);
  assert.equal(current.body.dataset, dataset);
  assert.deepEqual(current.body.classList.values(), ["center-screen", "theme-dark"]);
  assert.equal(current.notices["update-notice"], ready.notices["update-notice"]);
  assert.equal(current.notices["settings-update"], ready.notices["settings-update"]);

  const loading = documentFixture("loading-again", ["center"]);
  assert.equal(replaceLive(current, loading), true);
  assert.equal(current.live.label, "loading-again");
  assert.equal(current.getElementById("btc-price"), price);
  assert.equal(current.canvas, canvas);
  assert.equal(current.walletForm, walletForm);
  assert.equal(current.settings, settings);
  assert.equal(current.getElementById("tower-stats"), towerStats);
  assert.equal(current.getElementById("watch"), watch);
  assert.equal(current.body.dataset, dataset);
  assert.deepEqual(current.body.classList.values(), ["center", "theme-dark"]);
});

test("live refresh reports a missing panel without changing the page", () => {
  const current = documentFixture("current");
  const missing = documentFixture("missing", [], false);
  const original = current.live;
  const price = current.live.price;
  const towerStats = current.persistent["tower-stats"];
  const watch = current.persistent.watch;
  const notice = current.notices["update-notice"];
  assert.equal(replaceLive(current, missing), false);
  assert.equal(current.live, original);
  assert.equal(current.getElementById("btc-price"), price);
  assert.equal(current.getElementById("tower-stats"), towerStats);
  assert.equal(current.getElementById("watch"), watch);
  assert.equal(current.notices["update-notice"], notice);
  assert.equal(replaceLive(missing, current), false);
});

test("release checks refresh the overview notice and Settings instructions", () => {
  const replaced = [];
  const current = { getElementById: (id) => ({ replaceWith: (next) => replaced.push([id, next.id]) }) };
  const next = { getElementById: (id) => ({ id: `next-${id}` }) };
  replaceUpdateViews(current, next);
  assert.deepEqual(replaced, [
    ["update-notice", "next-update-notice"],
    ["settings-update", "next-settings-update"],
  ]);
});
