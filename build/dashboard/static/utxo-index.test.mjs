import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { badgeClickTarget, indexBadge, indexView, indexBarView, applyIndexBar, offHint, OFF_HINT, OFF_HINT_DOCS, SEED_COMMANDS, CONFIRMED_ONLY } from "./utxo-index.js";

test("every endpoint state renders its label, and the confirmed-only row is always present", () => {
  const cases = [
    [{ enabled: true, state: "not_seeded" }, "Index: Not seeded", ""],
    [{ enabled: true, state: "catching_up" }, "Index: Catching up", ""],
    [{ enabled: true, state: "ready" }, "Index: Ready", ""],
    [{ enabled: true, state: "degraded", reason: "node unreachable" }, "Index: Degraded (node unreachable)", ""],
    [{ enabled: true, state: "invalid", reason: "chain reorganised" }, "Index: Needs a new seed (chain reorganised)", ""],
    [{ enabled: true, state: "seeding", phase: "merging", seed_progress: 0.5 }, "Index: Seeding", ""],
  ];
  for (const [status, first, command] of cases) {
    const view = indexView(status);
    assert.equal(view.rows[0], first);
    assert.equal(view.rows[1], CONFIRMED_ONLY);
    assert.equal(view.command, command);
  }
  assert.match(CONFIRMED_ONLY, /confirmed only/);
  assert.match(CONFIRMED_ONLY, /mempool/);
});

test("hidden when the index is off or the status is missing", () => {
  assert.equal(indexView(null), null);
  assert.equal(indexView({ enabled: false, state: "off" }), null);
  assert.equal(indexView({ enabled: true, state: "off" }), null);
});

test("an unseeded index shows every refusal, and the host command only when no helper can start it", () => {
  const status = {
    enabled: true, state: "not_seeded", refusals: [
      { guard: "thermal", detail: "hottest zone 80 C is above 75 C" },
      { guard: "free_space", detail: "3.0 GB free, 27 GB needed" },
    ],
  };
  const view = indexView(status);
  assert.equal(view.command, "");
  assert.equal(view.warning, undefined);
  assert.deepEqual(view.refusals, ["thermal: hottest zone 80 C is above 75 C", "free_space: 3.0 GB free, 27 GB needed"]);
  assert.equal(view.progress, null);
  assert.equal(indexView({ ...status, needs_host: true }).command, SEED_COMMANDS.bitcoin);
  assert.equal(indexView({ ...status, needs_host: true }, "bitcoincash").command, "./stack utxo-seed bitcoincash");
  assert.equal(indexView({ ...status, state: "seeding", needs_host: true }).command, "");
});

test("seeding shows phase, progress and the alert hold, and no command", () => {
  const view = indexView({
    enabled: true, state: "seeding", phase: "importing", seed_progress: 0.425,
    alerts_suppressed_until: "2026-11-14T12:15:00Z",
  });
  assert.equal(view.rows[1], CONFIRMED_ONLY);
  assert.equal(view.rows[2], "Seed: importing 42%");
  assert.match(view.rows[3], /Node-down alerts are held until/);
  assert.equal(view.progress, 0.425);
  assert.equal(view.command, "");
  assert.equal(indexView({ enabled: true, state: "seeding" }).rows[2], "Seed: waiting");
});

test("a failed or cancelled last seed is reported; a ready index shows no command", () => {
  const failed = indexView({ enabled: true, state: "not_seeded", last_seed: { outcome: "failed", reason: "less than 2 GB free during the seed" } });
  assert.match(failed.rows.at(-1), /Last seed failed: less than 2 GB free/);
  assert.match(indexView({ enabled: true, state: "invalid", reason: "x", last_seed: { outcome: "cancelled" } }).rows.at(-1), /Last seed cancelled\./);
  const ready = indexView({ enabled: true, state: "ready", last_seed: { outcome: "ok" } });
  assert.deepEqual(ready.rows, ["Index: Ready", CONFIRMED_ONLY]);
  assert.equal(ready.command, "");
});

test("the off hint shows only for an off index on a pruned node", () => {
  assert.equal(offHint({ enabled: false, state: "off", node_pruned: true }), OFF_HINT);
  assert.match(OFF_HINT, /Settings/);
  assert.match(OFF_HINT_DOCS, /^https:\/\/github\.com\/.*docs\/watch-only\.md$/);
  assert.equal(offHint({ enabled: false, state: "off", node_pruned: false }), "");
  assert.equal(offHint({ enabled: false, state: "off", node_pruned: null }), "");
  assert.equal(offHint({ enabled: false, state: "off" }), "");
  assert.equal(offHint(null), "");
  assert.equal(offHint({ enabled: true, state: "ready", node_pruned: true }), "");
  assert.equal(indexView({ enabled: false, state: "off", node_pruned: true }), null);
  assert.equal(indexView({ enabled: true, state: "ready" }).rows[0], "Index: Ready");
});

test("the wallet card badge names each index state and its tone", () => {
  const cases = [
    [{ enabled: true, state: "ready" }, "Balance index: ready", "ok"],
    [{ enabled: true, state: "catching_up" }, "Balance index: catching up", "info"],
    [{ enabled: true, state: "seeding" }, "Balance index: seeding", "info"],
    [{ enabled: true, state: "not_seeded" }, "Balance index: needs a seed", "warn"],
    [{ enabled: true, state: "invalid" }, "Balance index: needs a seed", "warn"],
    [{ enabled: true, state: "degraded" }, "Balance index: degraded", "bad"],
    [{ enabled: false, state: "off" }, "Balance index: off", "off"],
    [{ enabled: true, state: "off" }, "Balance index: off", "off"],
  ];
  for (const [status, text, tone] of cases) assert.deepEqual(indexBadge(status), { text, tone });
  assert.equal(indexBadge(null), null);
});

test("the badge scrolls to the index card when it shows and opens Settings when it is hidden", () => {
  assert.equal(badgeClickTarget(false), "anchor");
  assert.equal(badgeClickTarget(true), "settings");
});

test("the index card has no Seed now button and no per-chain warning text", () => {
  for (const chain of ["bitcoin", "litecoin", "bitcoincash", "dogecoin"]) {
    const view = indexView({ enabled: true, state: "not_seeded", seed_now: true }, chain);
    assert.equal(view.seedNow, undefined);
    assert.equal(view.warning, undefined);
  }
});

test("the node card bar follows the index state", () => {
  const base = { enabled: true };
  const view = (status) => indexBarView({ ...base, ...status });
  assert.deepEqual(indexBarView(null), { hidden: true });
  assert.deepEqual(indexBarView({ enabled: false, state: "off" }), { hidden: true });
  assert.deepEqual(indexBarView({ enabled: true, state: "off" }), { hidden: true });
  assert.deepEqual(view({ state: "not_seeded", waiting_for: "node_sync" }), { hidden: false, label: "Balance index: waiting for the node to sync", value: null, tone: "info" });
  for (const waiting of ["node_start", "other_seed", "host_busy"]) assert.equal(view({ state: "not_seeded", waiting_for: waiting }).label, "Balance index: waiting to start");
  assert.equal(view({ state: "not_seeded", waiting_for: "" }).label, "Balance index: starting");
  assert.equal(view({ state: "not_seeded", waiting_for: "blocked", refusals: [{ guard: "cpu_cap", detail: "cpu limit is above 0.5" }] }).label, "Balance index: waiting to start: cpu limit is above 0.5");
  assert.equal(view({ state: "not_seeded", waiting_for: "blocked", refusals: [{ guard: "cpu_cap", detail: "x" }] }).tone, "warn");
  assert.deepEqual(view({ state: "not_seeded", needs_host: true }), { hidden: false, label: "Balance index: needs the host helper", value: null, tone: "warn" });
  assert.deepEqual(view({ state: "seeding", seed_progress: 0.425, phase: "importing" }), { hidden: false, label: "Balance index: building, 42%", value: 0.425, tone: "info" });
  assert.equal(view({ state: "seeding", seed_progress: 0.5, phase: "paused" }).label, "Balance index: paused, 50%");
  assert.deepEqual(view({ state: "seeding", seed_progress: null, phase: "waiting" }), { hidden: false, label: "Balance index: building", value: null, tone: "info" });
  assert.deepEqual(view({ state: "catching_up", blocks_behind: 120, index_tip_height: 880, node_tip_height: 1000 }), { hidden: false, label: "Balance index: catching up 120 blocks", value: null, tone: "info" });
  assert.deepEqual(view({ state: "ready" }), { hidden: false, label: "Balance index: ready", value: 1, tone: "ok" });
  assert.deepEqual(view({ state: "degraded", reason: "node unreachable" }), { hidden: false, label: "Balance index: needs attention: node unreachable", value: null, tone: "warn" });
  assert.equal(view({ state: "invalid", reason: "x".repeat(200) }).label.length < 120, true);
  const stopped = view({ state: "not_seeded", seed_stopped: "failed", last_seed: { outcome: "failed", reason: "less than 2 GB free during the seed" } });
  assert.deepEqual(stopped, { hidden: false, label: "Balance index: seeding failed: less than 2 GB free during the seed", value: null, tone: "warn" });
  assert.equal(view({ state: "not_seeded", seed_stopped: "cancelled" }).label, "Balance index: seeding stopped");
});

test("applyIndexBar writes the label, the width and the accessible value", () => {
  const attrs = {};
  const bar = {
    hidden: true, className: "",
    label: { textContent: "" }, fill: { style: { width: "0%" } },
    track: { setAttribute: (k, v) => { attrs[k] = v; }, removeAttribute: (k) => { delete attrs[k]; } },
    querySelector(selector) { return selector === ".index-bar-label" ? this.label : selector === ".index-bar-fill" ? this.fill : this.track; },
  };
  applyIndexBar(bar, indexBarView({ enabled: true, state: "seeding", seed_progress: 0.425 }));
  assert.equal(bar.hidden, false);
  assert.equal(bar.label.textContent, "building, 42%");
  assert.equal(bar.fill.style.width, "43%");
  assert.equal(attrs["aria-valuenow"], "43");
  assert.match(bar.className, /index-bar-determinate index-bar-info/);
  applyIndexBar(bar, indexBarView({ enabled: true, state: "not_seeded", waiting_for: "node_sync" }));
  assert.match(bar.className, /index-bar-indeterminate/);
  assert.equal("aria-valuenow" in attrs, false);
  applyIndexBar(bar, indexBarView({ enabled: true, state: "off" }));
  assert.equal(bar.hidden, true);
  applyIndexBar(null, { hidden: true });
});

test("every node card carries a hidden bar, the Bitcoin one inside the replaced live block, redrawn on live-updated", async () => {
  const [html, source] = await Promise.all(["../index.html", "./utxo-index.js"].map((file) => readFile(new URL(file, import.meta.url), "utf8")));
  for (const chain of ["bitcoin", "litecoin", "dogecoin", "bitcoincash"]) assert.match(html, new RegExp(`id="index-bar-${chain}" class="index-bar" hidden`));
  const live = html.slice(html.lastIndexOf('<div id="live"'), html.lastIndexOf('<section id="watch"'));
  assert.ok(live.includes('id="index-bar-bitcoin"'));
  assert.match(source, /addEventListener\("live-updated", renderBar\)/);
});
