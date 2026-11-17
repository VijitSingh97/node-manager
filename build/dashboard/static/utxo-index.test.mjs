import test from "node:test";
import assert from "node:assert/strict";
import { badgeClickTarget, indexBadge, indexView, offHint, requestSeed, OFF_HINT, OFF_HINT_DOCS, SEED_COMMAND, SEED_WARNING, LITECOIN_SEED_COMMAND, LITECOIN_SEED_WARNING, BITCOINCASH_SEED_COMMAND, BITCOINCASH_SEED_WARNING, DOGECOIN_SEED_COMMAND, DOGECOIN_SEED_WARNING, CONFIRMED_ONLY } from "./utxo-index.js";

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

test("Seed now replaces the host command when the host can prepare a seed", async () => {
  for (const state of ["not_seeded", "invalid"]) {
    const view = indexView({ enabled: true, state, seed_now: true });
    assert.equal(view.seedNow, true);
    assert.equal(view.command, "");
  }
  assert.equal(indexView({ enabled: true, state: "not_seeded" }).seedNow, false);
  assert.equal(indexView({ enabled: true, state: "seeding", seed_now: true }).seedNow, false);
  let sent;
  const reply = (ok, text) => async (url, init) => { sent = { url, init }; return { ok, text: async () => text }; };
  assert.match(await requestSeed(reply(true, "")), /Seed requested/);
  assert.equal(sent.url, "/api/settings/host");
  assert.deepEqual(JSON.parse(sent.init.body), { action: "utxo_seed", node: "bitcoin" });
  assert.equal(await requestSeed(reply(false, "The balance index cannot be seeded now.\n")), "The balance index cannot be seeded now.");
  assert.equal(await requestSeed(async () => { throw new Error("offline"); }), "Could not request a seed.");
});

test("the Litecoin index card shows its own command and warning and seeds Litecoin", async () => {
  const view = indexView({ enabled: true, state: "not_seeded" }, "litecoin");
  assert.equal(view.command, LITECOIN_SEED_COMMAND);
  assert.equal(view.warning, LITECOIN_SEED_WARNING);
  assert.equal(indexView({ enabled: true, state: "not_seeded" }).command, SEED_COMMAND);
  let body;
  await requestSeed(async (url, init) => { body = JSON.parse(init.body); return { ok: true, text: async () => "" }; }, "litecoin");
  assert.deepEqual(body, { action: "utxo_seed", node: "litecoin" });
});

test("the Bitcoin Cash index card states the archival replay and seeds Bitcoin Cash", async () => {
  const view = indexView({ enabled: true, state: "not_seeded" }, "bitcoincash");
  assert.equal(view.command, BITCOINCASH_SEED_COMMAND);
  assert.equal(view.warning, BITCOINCASH_SEED_WARNING);
  assert.match(BITCOINCASH_SEED_WARNING, /replays every block from genesis on an archival node/);
  let body;
  await requestSeed(async (url, init) => { body = JSON.parse(init.body); return { ok: true, text: async () => "" }; }, "bitcoincash");
  assert.deepEqual(body, { action: "utxo_seed", node: "bitcoincash" });
});

test("the Dogecoin index card states the chainstate-copy seed and seeds Dogecoin", async () => {
  const view = indexView({ enabled: true, state: "not_seeded" }, "dogecoin");
  assert.equal(view.command, DOGECOIN_SEED_COMMAND);
  assert.equal(view.warning, DOGECOIN_SEED_WARNING);
  assert.match(DOGECOIN_SEED_WARNING, /works on a pruned node/);
  let body;
  await requestSeed(async (url, init) => { body = JSON.parse(init.body); return { ok: true, text: async () => "" }; }, "dogecoin");
  assert.deepEqual(body, { action: "utxo_seed", node: "dogecoin" });
});
