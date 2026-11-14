import test from "node:test";
import assert from "node:assert/strict";
import { indexView, SEED_COMMAND, SEED_WARNING } from "./utxo-index.js";

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
  assert.match(view.warning, /at least 15 minutes/);
  assert.match(view.warning, /27 GB/);
  assert.deepEqual(view.refusals, ["thermal: hottest zone 80 C is above 75 C", "free_space: 3.0 GB free, 27 GB needed"]);
  assert.equal(view.progress, null);
});

test("seeding shows phase, progress and the alert hold, and no command", () => {
  const view = indexView({
    enabled: true, state: "seeding", phase: "importing", seed_progress: 0.425,
    alerts_suppressed_until: "2026-11-14T12:15:00Z",
  });
  assert.equal(view.rows[1], "Seed: importing 42%");
  assert.match(view.rows[2], /Node-down alerts are held until/);
  assert.equal(view.progress, 0.425);
  assert.equal(view.command, "");
  assert.equal(view.warning, SEED_WARNING);
  assert.equal(indexView({ enabled: true, state: "seeding" }).rows[1], "Seed: waiting");
});

test("a failed or cancelled last seed is reported; a ready index shows no command", () => {
  const failed = indexView({ enabled: true, state: "not_seeded", last_seed: { outcome: "failed", reason: "less than 2 GB free during the seed" } });
  assert.match(failed.rows.at(-1), /Last seed failed: less than 2 GB free/);
  assert.match(indexView({ enabled: true, state: "invalid", reason: "x", last_seed: { outcome: "cancelled" } }).rows.at(-1), /Last seed cancelled\./);
  const ready = indexView({ enabled: true, state: "ready", last_seed: { outcome: "ok" } });
  assert.equal(ready.rows.length, 1);
  assert.equal(ready.command, "");
  assert.equal(ready.warning, "");
});
