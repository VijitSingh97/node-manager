import test from "node:test";
import assert from "node:assert/strict";
import { diskView, NODE_COLORS } from "./host-stats.js";

const GiB = 1024 ** 3;

test("diskView splits the bar into node, other, and free slices", () => {
  const host = { disk_available: true, disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB };
  const view = diskView(host, new Map([["bitcoin", 10 * GiB], ["monero", 30 * GiB]]));
  assert.equal(view.line, "Disk 1000.00 GiB · 60.0% used");
  assert.deepEqual(view.slices.map((s) => s.id), ["bitcoin", "monero", "other", "free"]);
  assert.equal(view.slices[0].color, NODE_COLORS.bitcoin);
  assert.equal(Math.round(view.slices[0].percent * 100) / 100, 1);
  assert.equal(Math.round(view.slices[2].percent), 56);
  assert.equal(Math.round(view.slices[3].percent), 40);
  assert.match(view.slices[1].title, /Monero — 30\.00 GiB \(3\.00%\)/);
});

test("diskView rejects missing or unusable host data and zero-size nodes", () => {
  assert.equal(diskView(null, new Map()), null);
  assert.equal(diskView({ disk_available: false, disk_total_bytes: GiB }, new Map()), null);
  assert.equal(diskView({ disk_available: true, disk_total_bytes: 0 }, new Map()), null);
  const view = diskView({ disk_available: true, disk_total_bytes: 100 * GiB, disk_free_bytes: 200 * GiB }, new Map([["tari", 0]]));
  assert.deepEqual(view.slices.map((s) => s.id), ["other", "free"]);
  assert.equal(view.slices[1].percent, 100);
});
