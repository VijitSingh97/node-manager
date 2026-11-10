import test from "node:test";
import assert from "node:assert/strict";
import { diskViews, zoomSlices, pieView, rescaleToStack, arcPath, withCollectorSizes, NODE_COLORS, OTHER_COLOR } from "./host-stats.js";

const GiB = 1024 ** 3;

test("diskViews splits a single disk into node, other, and free slices", () => {
  const host = { disk_available: true, disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB };
  const view = diskViews(host, new Map([["bitcoin", 10 * GiB], ["monero", 30 * GiB]]));
  assert.equal(view.line, "Disk 60% used");
  assert.match(view.lineTitle, /600\.00 GiB of 1000\.00 GiB used · 400\.00 GiB free/);
  assert.equal(view.bars.length, 1);
  assert.deepEqual(view.bars[0].slices.map((s) => s.id), ["bitcoin", "monero", "other", "free"]);
  assert.equal(view.bars[0].slices[0].color, NODE_COLORS.bitcoin);
  assert.equal(Math.round(view.bars[0].slices[2].percent), 56);
  assert.match(view.bars[0].slices[1].title, /Monero — 30\.00 GiB \(3\.00%\)/);
});

test("diskViews renders one bar per disk with nodes attributed to their disk", () => {
  const host = {
    disk_available: true,
    disk_total_bytes: 1000 * GiB,
    disk_free_bytes: 400 * GiB,
    disks: [
      { disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB, nodes: ["bitcoin"] },
      { disk_total_bytes: 500 * GiB, disk_free_bytes: 100 * GiB, nodes: ["monero"] },
    ],
  };
  const view = diskViews(host, new Map([["bitcoin", 10 * GiB], ["monero", 30 * GiB]]));
  assert.equal(view.line, "Disks 67% used");
  assert.equal(view.bars.length, 2);
  assert.deepEqual(view.bars[0].slices.map((s) => s.id), ["bitcoin", "other", "free"]);
  assert.deepEqual(view.bars[1].slices.map((s) => s.id), ["monero", "other", "free"]);
  assert.match(view.bars[1].title, /Disk 2 — 80% used/);
  assert.equal(Math.round(view.bars[1].slices[0].percent), 6);
});

test("diskViews hides disks without node-manager data but keeps stack-only disks", () => {
  const host = {
    disk_available: true, disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB,
    disks: [
      { name: "/dev/nvme0n1p2", disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB, nodes: ["bitcoin", "stack"] },
      { name: "/dev/sda1", disk_total_bytes: 500 * GiB, disk_free_bytes: 450 * GiB, nodes: [] },
    ],
  };
  const view = diskViews(host, new Map([["bitcoin", 10 * GiB]]));
  assert.equal(view.bars.length, 1);
  assert.equal(view.bars[0].label, "/dev/nvme0n1p2");
  const stackOnly = diskViews({ ...host, disks: [host.disks[1], { ...host.disks[1], name: "/dev/sdb1", nodes: ["stack"] }] }, new Map());
  assert.equal(stackOnly.bars.length, 1);
  assert.equal(stackOnly.bars[0].label, "/dev/sdb1");
});

test("diskViews rejects unusable host data and zero-size nodes", () => {
  assert.equal(diskViews(null, new Map()), null);
  assert.equal(diskViews({ disk_available: false, disk_total_bytes: GiB }, new Map()), null);
  assert.equal(diskViews({ disk_available: true, disk_total_bytes: 0 }, new Map()), null);
  const view = diskViews({ disk_available: true, disk_total_bytes: 100 * GiB, disk_free_bytes: 200 * GiB }, new Map([["tari", 0]]));
  assert.deepEqual(view.bars[0].slices.map((s) => s.id), ["other", "free"]);
  assert.equal(view.bars[0].slices[1].percent, 100);
});

test("zoomSlices rescales node slices to the filtered node-data total", () => {
  const slices = zoomSlices(new Map([["bitcoin", 10 * GiB], ["monero", 30 * GiB], ["tari", 0]]), null);
  assert.deepEqual(slices.map((s) => [s.id, Math.round(s.percent)]), [["bitcoin", 25], ["monero", 75]]);
  assert.match(slices[1].title, /75\.00% of Node Manager data/);
  const onDisk = zoomSlices(new Map([["bitcoin", 10 * GiB]]), null, 1000 * GiB);
  assert.match(onDisk[0].title, /Bitcoin — 10\.00 GiB \(1\.00% of disk\)/);
  assert.deepEqual(zoomSlices(new Map([["bitcoin", 10 * GiB]]), ["monero"]), null);
  assert.equal(zoomSlices(new Map(), null), null);
});

test("diskViews zooms each bar independently", () => {
  const host = {
    disk_available: true, disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB,
    disks: [
      { disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB, nodes: ["bitcoin"] },
      { disk_total_bytes: 500 * GiB, disk_free_bytes: 100 * GiB, nodes: ["monero"] },
    ],
  };
  const sizes = new Map([["bitcoin", 10 * GiB], ["monero", 30 * GiB]]);
  const view = diskViews(host, sizes, new Set([1]));
  assert.equal(view.bars[0].zoomed, false);
  assert.deepEqual(view.bars[0].slices.map((s) => s.id), ["bitcoin", "other", "free"]);
  assert.equal(view.bars[1].zoomed, true);
  assert.deepEqual(view.bars[1].slices.map((s) => [s.id, s.percent]), [["monero", 100]]);
  assert.match(view.bars[1].slices[0].title, /Monero — 30\.00 GiB \(6\.00% of disk\)/);
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
  assert.equal(view.cpuLabelTitle, "2.8 of 8 cores");
  assert.equal(view.memLabel, "Mem 44%");
  assert.equal(view.memLabelTitle, "7.0 of 16.0 GiB");
});

test("pieView rejects unusable stats and clamps overflow", () => {
  assert.equal(pieView(null), null);
  assert.equal(pieView({ cpus: 0, mem_total_bytes: GiB }), null);
  const view = pieView({ cpus: 1, mem_total_bytes: GiB, nodes: { bitcoin: { cpu_pct: 150, mem_bytes: 3 * GiB } }, other: {} });
  assert.equal(view.cpu[0].percent, 100);
  assert.equal(view.cpu.at(-1).percent, 0);
  assert.equal(view.mem[0].percent, 100);
});

test("rescaleToStack rebases one pie to stack usage", () => {
  const stats = { cpus: 8, mem_total_bytes: 16 * GiB, nodes: { bitcoin: { cpu_pct: 10, mem_bytes: 2 * GiB }, monero: { cpu_pct: 20, mem_bytes: 4 * GiB } }, other: { cpu_pct: 5, mem_bytes: GiB } };
  const cpu = rescaleToStack(pieView(stats).cpu);
  assert.deepEqual(cpu.map((s) => s.id), ["bitcoin", "monero", "other"]);
  assert.equal(Math.round(cpu[1].percent * 10) / 10, 57.1);
  assert.match(cpu[1].title, /57\.1% of stack/);
  assert.equal(Math.round(cpu.reduce((t, s) => t + s.percent, 0)), 100);
  assert.equal(rescaleToStack(pieView({ cpus: 4, mem_total_bytes: GiB, nodes: {}, other: {} }).cpu), null);
});

test("arcPath produces unit-circle arcs with the right large-arc flag", () => {
  assert.equal(arcPath(0, 0.25), "M 1.0000 0.0000 A 1 1 0 0 1 0.0000 1.0000 L 0 0 Z");
  assert.match(arcPath(0, 0.75), /A 1 1 0 1 1/);
  assert.match(arcPath(0, 1), /A 1 1 0 1 1 1 -0\.0002/);
});

test("withCollectorSizes seeds from collector disk bytes and lets live RPC sizes win", () => {
  const stats = { nodes: { bitcoin: { cpu_pct: 0, mem_bytes: 0, disk_bytes: 9 * GiB }, monero: { cpu_pct: 3, mem_bytes: GiB, disk_bytes: 28 * GiB } } };
  const merged = withCollectorSizes(stats, new Map([["monero", 30 * GiB], ["bitcoin", 0]]));
  assert.equal(merged.get("bitcoin"), 9 * GiB);
  assert.equal(merged.get("monero"), 30 * GiB);
  assert.equal(withCollectorSizes(null, new Map()).size, 0);
});

test("other and free slices use distinct colors on bars and pies", () => {
  const host = { disk_available: true, disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB };
  const bar = diskViews(host, new Map([["bitcoin", 100 * GiB]])).bars[0];
  const other = bar.slices.find((s) => s.id === "other");
  const free = bar.slices.find((s) => s.id === "free");
  assert.equal(other.color, OTHER_COLOR);
  assert.notEqual(other.color, free.color);
  const view = pieView({ cpus: 4, mem_total_bytes: 8 * GiB, nodes: {}, other: { cpu_pct: 5, mem_bytes: GiB } });
  assert.equal(view.cpu.find((s) => s.id === "other").color, OTHER_COLOR);
  assert.notEqual(view.cpu.find((s) => s.id === "other").color, view.cpu.find((s) => s.id === "idle").color);
});
