import test from "node:test";
import assert from "node:assert/strict";
import { applyHover, formatBytes, formatCompactBytes, formatHashrate, formatMoneroFees, formatUpdated, formatUptime, genericNodeView, moneroView, selectionController, updateTowerProgress } from "./nodes.js";

test("tower progress follows fresh node status and survives temporary outages or deactivation", () => {
  const live = { dataset: {} };
  updateTowerProgress(live, { enabled: true, available: false });
  assert.equal(live.dataset.syncPercent, undefined);
  updateTowerProgress(live, { enabled: true, available: true, sync_percent: 23.75 });
  assert.equal(live.dataset.syncPercent, "23.75");
  for (const data of [{ available: false }, { available: true, enabled: false, sync_percent: 0 },
    { available: true }, { available: true, sync_percent: null }, { available: true, sync_percent: NaN }]) {
    updateTowerProgress(live, data);
    assert.equal(live.dataset.syncPercent, "23.75");
  }
  updateTowerProgress(live, { available: true, sync_percent: 48.5 });
  assert.equal(live.dataset.syncPercent, "48.5");
  updateTowerProgress(live, { available: true, synced: true });
  assert.equal(live.dataset.syncPercent, "100");
  updateTowerProgress(live, { available: true, sync_percent: -1 });
  assert.equal(live.dataset.syncPercent, "0");
  updateTowerProgress(live, { available: true, sync_percent: 101 });
  assert.equal(live.dataset.syncPercent, "100");
});

function dashboard() {
  const overview = { hidden: false, scrollTop: 0, scrollLeft: 0 };
  const viewport = { scrollX: 0, scrollY: 0, scrollTo(left, top) { this.scrollX = left; this.scrollY = top; } };
  const focusTarget = () => ({ focused: 0, focus(options) { this.focused++; if (!options?.preventScroll) viewport.scrollY = 600; } });
  const bitcoinClose = focusTarget();
  const moneroClose = focusTarget();
  const panels = [
    { hidden: true, dataset: { nodePanel: "bitcoin" } },
    { hidden: true, dataset: { nodePanel: "monero" } },
  ];
  const choices = ["bitcoin", "monero"].map((node) => ({ dataset: { selectNode: node },
    setAttribute(name, value) { this[name] = value; } }));
  return {
    body: { dataset: { node: "" }, scrollTop: 0, scrollLeft: 0 }, scrollingElement: { scrollTop: 0, scrollLeft: 0 }, documentElement: { scrollTop: 0, scrollLeft: 0 }, defaultView: viewport, overview, panels, choices, bitcoinClose, moneroClose,
    getElementById: (id) => id === "node-overview" ? overview : null,
    querySelectorAll: (selector) => selector === "[data-node-panel]" ? panels : choices,
    querySelector: (selector) => selector.includes("bitcoin") ? bitcoinClose : moneroClose,
  };
}

test("tower selection opens one panel, survives refresh, and closes to the chosen tower", () => {
  const doc = dashboard();
  const events = [];
  const bitcoinChoice = { focused: 0, focus() { this.focused++; } };
  const controller = selectionController(doc, (node) => events.push(node));

  controller.open("bitcoin", bitcoinChoice);
  assert.equal(doc.body.dataset.node, "bitcoin");
  assert.equal(doc.overview.hidden, false);
  assert.deepEqual(doc.panels.map((panel) => panel.hidden), [false, true]);
  assert.deepEqual(doc.choices.map((choice) => choice["aria-expanded"]), ["true", "false"]);
  assert.equal(doc.bitcoinClose.focused, 1);

  doc.panels[0].hidden = true;
  controller.refresh();
  assert.equal(doc.panels[0].hidden, false);

  controller.close();
  assert.equal(doc.body.dataset.node, "");
  assert.equal(doc.overview.hidden, false);
  assert.deepEqual(doc.panels.map((panel) => panel.hidden), [true, true]);
  assert.deepEqual(doc.choices.map((choice) => choice["aria-expanded"]), ["false", "false"]);
  assert.equal(bitcoinChoice.focused, 1);
  assert.deepEqual(events, ["bitcoin", ""]);
});

test("returning from scrolled panels recenters the overview without moving its horizontal position", () => {
  const doc = dashboard();
  const controller = selectionController(doc);
  doc.overview.scrollLeft = 140;
  for (const node of ["bitcoin", "monero", "bitcoin", "monero"]) {
    const choice = { focus(options) { if (!options?.preventScroll) doc.overview.scrollTop = 400; } };
    doc.defaultView.scrollY = 900;
    controller.open(node, choice);
    assert.equal(doc.defaultView.scrollY, 0);
    doc.defaultView.scrollY = 1200;
    for (const root of [doc.scrollingElement, doc.documentElement, doc.body]) root.scrollTop = 1200;
    doc.overview.scrollTop = 450;
    controller.refresh();
    assert.equal(doc.defaultView.scrollY, 1200);
    assert.equal(doc.scrollingElement.scrollTop, 1200);
    controller.close();
    assert.equal(doc.body.dataset.node, "");
    assert.equal(doc.defaultView.scrollY, 0);
    for (const root of [doc.scrollingElement, doc.documentElement, doc.body]) assert.equal(root.scrollTop, 0);
    assert.equal(doc.overview.scrollTop, 0);
    assert.equal(doc.overview.scrollLeft, 140);
  }
});

test("tower hover only records supported nodes", () => {
  const doc = dashboard();
  applyHover(doc, "monero");
  assert.equal(doc.body.dataset.hoverNode, "monero");
  applyHover(doc, "other");
  assert.equal(doc.body.dataset.hoverNode, "");
});

test("Monero status does not invent metrics while disabled or unreachable", () => {
  assert.deepEqual(moneroView({ enabled: false }), { state: "Not configured", badge: "Disabled", details: false });
  assert.deepEqual(moneroView({ enabled: true, available: false, height: 99 }), { state: "Connecting...", badge: "Connecting", details: false });
});

test("zero-height nodes load block one without inventing a height when it is missing", () => {
  for (const view of [
    moneroView({ enabled: true, available: true, height: 0, sync_percent: 0 }),
    genericNodeView({ enabled: true, available: true, height: 0, sync_percent: 0 }, "Litecoin"),
  ]) {
    assert.equal(view.progress, 0);
    assert.equal(view.nextBlock, 1);
  }
  assert.equal(moneroView({ enabled: true, available: true, sync_percent: 0 }).nextBlock, "");
  assert.equal(genericNodeView({ enabled: true, available: true, height: null, sync_percent: 0 }, "Litecoin").nextBlock, "");
});

test("available Monero metrics are clamped and formatted for the status card", () => {
  const view = moneroView({ enabled: true, available: true, synced: false, height: 120, target_height: 125,
    sync_percent: 96, version: "0.18.4.2", uptime_seconds: 90061, uptime_available: true, incoming_connections: 2,
    outgoing_connections: 8, pruned: true, database_size_bytes: 10 * 1024 ** 3, database_size_available: true,
    disk_available: true, disk_total_bytes: 20 * 1024 ** 3, disk_free_bytes: 8 * 1024 ** 3,
    mempool_txs: 3, mempool_txs_available: true, mempool_bytes_available: true, mempool_bytes: 1536, difficulty: 1234567, difficulty_available: true,
    hashrate_available: true, network_hashrate: 1234567, latest_block_available: true,
    latest_block_age_seconds: 42, fees_available: true,
    fee_estimates_atomic_per_byte: [20_000, 80_000, 320_000, 4_000_000], updated_at: 1_700_000_000,
    rpc_exposure_enabled: true, rpc_endpoint: "192.168.1.2:18081" });
  assert.equal(view.state, "Synchronizing the Monero blockchain...");
  assert.equal(view.badge, "Pruned");
  assert.equal(view.blocks, "120 / 125");
  assert.equal(view.uptime, "1d 1h 1m");
  assert.equal(view.connections, "2 in · 8 out");
  assert.equal(view.size, "10.00 GiB");
  assert.equal(view.mempool, "3 tx · 1.5 KiB");
  assert.equal(view.difficulty, "1,234,567");
  assert.equal(view.hashrate, "1.23 MH/s");
  assert.equal(view.lastBlock, "<1m ago");
  assert.equal(view.fees, "Slow: 0.00002\nNormal: 0.00008\nFast: 0.00032\nFastest: 0.004");
  assert.equal(view.rpcExposure, "192.168.1.2:18081 · LAN");
  assert.equal(view.zmqExposure, "Disabled");
  assert.equal(formatUptime(-1), "0m");
  assert.equal(formatBytes(undefined), "—");
  assert.equal(formatCompactBytes(1024 ** 2), "1.0 MiB");
  assert.equal(formatHashrate(-1), "—");
  assert.equal(formatMoneroFees([1]), "—");
  assert.equal(formatUpdated(0), "—");
});

test("missing Monero mempool fields remain unavailable", () => {
  const view = moneroView({ enabled: true, available: true });
  assert.equal(view.mempool, "—");
  assert.equal(view.uptime, "—");
  assert.equal(view.size, "—");
  assert.equal(view.difficulty, "—");
  assert.equal(view.fees, "—");
});

test("generic node view warns only when the end-of-support flag is set", () => {
  const base = { enabled: true, available: true, height: 100, sync_percent: 100 };
  assert.equal(genericNodeView(base, "Zcash").warning, "");
  const view = genericNodeView({ ...base, end_of_support_warning: true, end_of_support_height: 3643309, end_of_support_remaining_blocks: 1000 }, "Zcash");
  assert.match(view.warning, /block 3,643,309 \(in 1,000 blocks\).*newer Zakura/);
  assert.match(genericNodeView({ ...base, end_of_support_warning: true, end_of_support_height: 3643309, end_of_support_remaining_blocks: -3 }, "Zcash").warning, /\(now\)/);
});
