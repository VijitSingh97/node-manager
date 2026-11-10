import test from "node:test";
import assert from "node:assert/strict";
import { diskView, pieView, arcPath, NODE_COLORS } from "./host-stats.js";

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

test("pieView builds node, other, and remainder slices for both pies", () => {
  const view = pieView({
    cpus: 8, mem_total_bytes: 16 * GiB,
    nodes: { bitcoin: { cpu_pct: 10, mem_bytes: 2 * GiB }, monero: { cpu_pct: 20, mem_bytes: 4 * GiB } },
    other: { cpu_pct: 5, mem_bytes: GiB },
  });
  assert.deepEqual(view.cpu.map((s) => s.id), ["bitcoin", "monero", "other", "idle"]);
  assert.equal(view.cpu.at(-1).percent, 65);
  assert.deepEqual(view.mem.map((s) => s.id), ["bitcoin", "monero", "other", "free"]);
  assert.equal(Math.round(view.mem[1].percent), 25);
  assert.match(view.mem[1].title, /Monero — 4\.00 GiB \(25\.0%\)/);
  assert.match(view.cpu[2].title, /Stack other — 5\.0% CPU/);
  assert.equal(view.cpuLabel, "CPU 35%");
  assert.equal(view.memLabel, "Mem 7.0 / 16.0 GiB");
});

test("pieView rejects unusable stats and clamps overflow", () => {
  assert.equal(pieView(null), null);
  assert.equal(pieView({ cpus: 0, mem_total_bytes: GiB }), null);
  const view = pieView({ cpus: 1, mem_total_bytes: GiB, nodes: { bitcoin: { cpu_pct: 150, mem_bytes: 3 * GiB } }, other: {} });
  assert.equal(view.cpu[0].percent, 100);
  assert.equal(view.cpu.at(-1).percent, 0);
  assert.equal(view.mem[0].percent, 100);
});

test("arcPath produces unit-circle arcs with the right large-arc flag", () => {
  assert.equal(arcPath(0, 0.25), "M 1.0000 0.0000 A 1 1 0 0 1 0.0000 1.0000 L 0 0 Z");
  assert.match(arcPath(0, 0.75), /A 1 1 0 1 1/);
  assert.match(arcPath(0, 1), /A 1 1 0 1 1 1 -0\.0002/);
});
