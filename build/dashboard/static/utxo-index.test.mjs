import test from "node:test";
import assert from "node:assert/strict";
import { indexView, offHint, OFF_HINT, OFF_HINT_DOCS, SEED_COMMAND, SEED_WARNING, CONFIRMED_ONLY } from "./utxo-index.js";

test("every endpoint state renders its label, and the confirmed-only row is always present", () => {
  const cases = [
    [{ enabled: true, state: "not_seeded" }, "Index: Not seeded", SEED_COMMAND],
    [{ enabled: true, state: "catching_up" }, "Index: Catching up", ""],
    [{ enabled: true, state: "ready" }, "Index: Ready", ""],
    [{ enabled: true, state: "degraded", reason: "node unreachable" }, "Index: Degraded (node unreachable)", ""],
    [{ enabled: true, state: "invalid", reason: "chain reorganised" }, "Index: Needs a new seed (chain reorganised)", SEED_COMMAND],
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

test("an unseeded index shows the host command, the warning and every refusal", () => {
  const view = indexView({
    enabled: true, state: "not_seeded", refusals: [
      { guard: "thermal", detail: "hottest zone 80 C is above 75 C" },
      { guard: "free_space", detail: "3.0 GB free, 27 GB needed" },
    ],
  });
  assert.equal(view.command, SEED_COMMAND);
  assert.equal(view.warning, SEED_WARNING);
  assert.match(view.warning, /about 20 minutes/);
  assert.match(view.warning, /stops answering for about 5 minutes/);
  assert.match(view.warning, /7\.5 GiB.*17 GiB/);
  assert.match(view.warning, /27 GB/);
  assert.deepEqual(view.refusals, ["thermal: hottest zone 80 C is above 75 C", "free_space: 3.0 GB free, 27 GB needed"]);
  assert.equal(view.progress, null);
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
  assert.equal(view.warning, SEED_WARNING);
  assert.equal(indexView({ enabled: true, state: "seeding" }).rows[2], "Seed: waiting");
});

test("a failed or cancelled last seed is reported; a ready index shows no command", () => {
  const failed = indexView({ enabled: true, state: "not_seeded", last_seed: { outcome: "failed", reason: "less than 2 GB free during the seed" } });
  assert.match(failed.rows.at(-1), /Last seed failed: less than 2 GB free/);
  assert.match(indexView({ enabled: true, state: "invalid", reason: "x", last_seed: { outcome: "cancelled" } }).rows.at(-1), /Last seed cancelled\./);
  const ready = indexView({ enabled: true, state: "ready", last_seed: { outcome: "ok" } });
  assert.deepEqual(ready.rows, ["Index: Ready", CONFIRMED_ONLY]);
  assert.equal(ready.command, "");
  assert.equal(ready.warning, "");
});

test("the off hint shows only for an off index on a pruned node", () => {
  assert.equal(offHint({ enabled: false, state: "off", node_pruned: true }), OFF_HINT);
  assert.match(OFF_HINT, /bitcoin\.utxo_index/);
  assert.match(OFF_HINT_DOCS, /^https:\/\/github\.com\/.*docs\/watch-only\.md$/);
  assert.equal(offHint({ enabled: false, state: "off", node_pruned: false }), "");
  assert.equal(offHint({ enabled: false, state: "off", node_pruned: null }), "");
  assert.equal(offHint({ enabled: false, state: "off" }), "");
  assert.equal(offHint(null), "");
  assert.equal(offHint({ enabled: true, state: "ready", node_pruned: true }), "");
  assert.equal(indexView({ enabled: false, state: "off", node_pruned: true }), null);
  assert.equal(indexView({ enabled: true, state: "ready" }).rows[0], "Index: Ready");
});
