import { readFileSync } from "node:fs";
import test from "node:test";
import assert from "node:assert/strict";
import { diskViews, zoomSlices, pieView, healthView, tempColor, fanTurnSeconds, uptimeRing, rescaleToStack, arcPath, withCollectorSizes, NODE_COLORS, OTHER_COLOR } from "./host-stats.js";

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

test("diskViews shows only disks whose attributed nodes hold actual bytes", () => {
  const host = {
    disk_available: true, disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB,
    disks: [
      { name: "/dev/nvme0n1p2", disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB, nodes: ["bitcoin", "stack"] },
      { name: "/dev/sda1", disk_total_bytes: 500 * GiB, disk_free_bytes: 450 * GiB, nodes: ["tari"] },
      { name: "/dev/sdc1", disk_total_bytes: 500 * GiB, disk_free_bytes: 450 * GiB, nodes: [] },
    ],
  };
  const view = diskViews(host, new Map([["bitcoin", 10 * GiB]]));
  assert.equal(view.bars.length, 1);
  assert.equal(view.bars[0].label, "/dev/nvme0n1p2");
  const stackOnly = diskViews({ ...host, disks: [host.disks[2], { ...host.disks[2], name: "/dev/sdb1", nodes: ["stack"] }] }, new Map([["stack", 30 * GiB]]));
  assert.equal(stackOnly.bars.length, 1);
  assert.equal(stackOnly.bars[0].label, "/dev/sdb1");
  assert.equal(diskViews(host, new Map()).bars.length, 3);
});

test("stack slice is labelled Node Manager itself and disks prefer model labels", () => {
  const host = {
    disk_available: true, disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB,
    disks: [
      { name: "/dev/mapper/vg-lv0", label: "Samsung SSD 990 PRO NVMe", disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB, nodes: ["bitcoin", "stack"] },
      { name: "/dev/sdb2", disk_total_bytes: 500 * GiB, disk_free_bytes: 100 * GiB, nodes: ["monero"] },
    ],
  };
  const sizes = withCollectorSizes({ nodes: { stack: { disk_bytes: 30 * GiB }, bitcoin: { disk_bytes: 10 * GiB }, monero: { disk_bytes: 100 * GiB } } }, new Map());
  const view = diskViews(host, sizes);
  assert.equal(view.bars[0].label, "Samsung SSD 990 PRO NVMe");
  assert.equal(view.bars[1].label, "/dev/sdb2");
  const stackSlice = view.bars[0].slices.find((s) => s.id === "stack");
  assert.equal(stackSlice.color, NODE_COLORS.stack);
  assert.match(stackSlice.title, /Node Manager itself — 30\.00 GiB/);
  const zoom = zoomSlices(sizes, ["bitcoin", "stack"], 1000 * GiB);
  assert.match(zoom.find((s) => s.id === "stack").title, /Node Manager itself/);
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
  assert.match(view.cpu[2].title, /Node Manager \(dashboard and Tor\) — 5\.0% CPU/);
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

test("withCollectorSizes prefers measured collector bytes and falls back to live RPC sizes", () => {
  const stats = { nodes: { bitcoin: { cpu_pct: 0, mem_bytes: 0, disk_bytes: 20 * GiB }, monero: { cpu_pct: 3, mem_bytes: GiB, disk_bytes: 107 * GiB } } };
  const merged = withCollectorSizes(stats, new Map([["monero", 100 * GiB], ["bitcoin", 10 * GiB], ["tari", 5 * GiB]]));
  assert.equal(merged.get("bitcoin"), 20 * GiB);
  assert.equal(merged.get("monero"), 107 * GiB);
  assert.equal(merged.get("tari"), 5 * GiB);
  assert.equal(withCollectorSizes(null, new Map()).size, 0);
});

test("pies show a grey other-processes slice from host-wide usage", () => {
  const view = pieView({
    cpus: 4, mem_total_bytes: 8 * GiB, host_cpu_pct: 40, host_mem_bytes: 4 * GiB,
    nodes: { bitcoin: { cpu_pct: 10, mem_bytes: GiB } }, other: { cpu_pct: 5, mem_bytes: GiB },
  });
  const cpuHost = view.cpu.find((s) => s.id === "host");
  assert.equal(cpuHost.color, OTHER_COLOR);
  assert.equal(Math.round(cpuHost.percent), 25);
  assert.match(cpuHost.title, /Other processes — 25\.0% CPU/);
  assert.equal(Math.round(view.cpu.find((s) => s.id === "idle").percent), 60);
  const memHost = view.mem.find((s) => s.id === "host");
  assert.equal(Math.round(memHost.percent), 25);
  assert.match(memHost.title, /Other processes — 2\.00 GiB \(25\.0%\)/);
  assert.equal(rescaleToStack(view.cpu).find((s) => s.id === "host"), undefined);
  const without = pieView({ cpus: 4, mem_total_bytes: 8 * GiB, nodes: {}, other: { cpu_pct: 5, mem_bytes: GiB } });
  assert.equal(without.cpu.find((s) => s.id === "host"), undefined);
});

test("other and free slices use distinct colors on bars and pies", () => {
  const host = { disk_available: true, disk_total_bytes: 1000 * GiB, disk_free_bytes: 400 * GiB };
  const bar = diskViews(host, new Map([["bitcoin", 100 * GiB]])).bars[0];
  const other = bar.slices.find((s) => s.id === "other");
  const free = bar.slices.find((s) => s.id === "free");
  assert.equal(other.color, OTHER_COLOR);
  assert.notEqual(other.color, free.color);
  const view = pieView({ cpus: 4, mem_total_bytes: 8 * GiB, nodes: {}, other: { cpu_pct: 5, mem_bytes: GiB } });
  assert.equal(view.cpu.find((s) => s.id === "other").color, NODE_COLORS.stack);
  assert.notEqual(view.cpu.find((s) => s.id === "other").color, view.cpu.find((s) => s.id === "idle").color);
});

test("healthView formats temperatures, spinning fans, uptime and network rate", () => {
  const rows = (stats) => healthView(stats).map(({ label, value }) => ({ label, value }));
  assert.deepEqual(rows({ cpu_temp_c: 59.4, board_temp_c: 45, fans_rpm: [0, 2506], uptime_s: 90061, net_rx_bps: 1536, net_tx_bps: 0 }), [
    { label: "Temperature", value: "CPU 59 °C · Board 45 °C" },
    { label: "Fan", value: "2506 rpm" },
    { label: "Uptime", value: "1d 1h 1m" },
    { label: "Network", value: "↓ 1.5 KiB/s · ↑ 0 B/s" },
  ]);
  const view = healthView({ board_temp_c: 41, fans_rpm: [1200, 0, 800.4], net_rx_bps: 3 * 1024 ** 2, net_tx_bps: 512 });
  assert.equal(view[0].value, "Board 41 °C");
  assert.deepEqual(view[1].label + view[1].value, "Fans1200 · 800 rpm");
  assert.equal(view[3].value, "↓ 3.0 MiB/s · ↑ 512 B/s");
  assert.equal(healthView({ fans_rpm: [0, 0] })[1].value, "0 rpm");
});

test("healthView carries the graphic data for each tile", () => {
  const [temp, fan, uptime, network] = healthView({ cpu_temp_c: 71, board_temp_c: 45, fans_rpm: [2497], fans_max_rpm: [4825], uptime_s: 2 * 86400 + 14 * 3600 + 50 * 60, net_rx_bps: 954163, net_tx_bps: 322970 });
  assert.deepEqual(temp.lines, ["CPU 71 °C", "Board 45 °C"]);
  assert.equal(temp.color, tempColor(71));
  assert.equal(fan.turnSeconds, fanTurnSeconds(2497, 4825));
  assert.deepEqual(uptime.ring.days, 2);
  assert.equal(uptime.value, "2d 14h 50m");
  assert.deepEqual(network.rates.map((rate) => rate.dir), ["down", "up"]);
  assert.equal(network.rates[0].text, "↓ 931.8 KiB/s");
  assert.equal(network.rates[1].text, "↑ 315.4 KiB/s");
  const idle = healthView({ fans_rpm: [0, 0] })[1];
  assert.equal(idle.turnSeconds, 0);
  assert.equal(healthView({ fans_rpm: [0, 1500], fans_max_rpm: [4000, 3000] })[1].turnSeconds, 0.7);
  assert.equal(healthView({})[0].color, "");
});

test("tempColor runs from blue when cool to red at the thermal limit", () => {
  assert.equal(tempColor(20), "hsl(215 85% 52%)");
  assert.equal(tempColor(40), "hsl(215 85% 52%)");
  assert.equal(tempColor(88), "hsl(0 85% 52%)");
  assert.equal(tempColor(120), "hsl(0 85% 52%)");
  assert.equal(tempColor(64), "hsl(288 85% 52%)");
  assert.equal(tempColor(60, 80), "hsl(288 85% 52%)");
  assert.equal(tempColor(64, 30), tempColor(64));
});

test("the reduced-motion rule stops the spin and the pulse", () => {
  const css = readFileSync(new URL("./dashboard.css", import.meta.url), "utf8");
  assert.match(css, /@media \(prefers-reduced-motion: reduce\) \{\s*\.vital-dot, \.vital-fan\.spinning \{ animation: none; \}/);
});

test("fanTurnSeconds is still at 0 rpm and one turn per 0.35 s at the fan maximum", () => {
  assert.equal(fanTurnSeconds(0, 4825), 0);
  assert.equal(fanTurnSeconds(4825, 4825), 0.35);
  assert.equal(fanTurnSeconds(9000, 4825), 0.35);
  assert.equal(fanTurnSeconds(2412.5, 4825), 0.7);
  assert.equal(fanTurnSeconds(2500, 0), 0.7);
  assert.equal(fanTurnSeconds(2500), 0.7);
  assert.equal(fanTurnSeconds(NaN, 4825), 0);
});

test("uptimeRing fills over each day and counts whole days", () => {
  assert.deepEqual(uptimeRing(0), { days: 0, fill: 0 });
  assert.deepEqual(uptimeRing(43200), { days: 0, fill: 0.5 });
  assert.deepEqual(uptimeRing(86400 + 21600), { days: 1, fill: 0.25 });
});

test("healthView says not available for each missing sensor", () => {
  for (const stats of [null, {}, { cpu_temp_c: null, board_temp_c: "", fans_rpm: [], uptime_s: 0, net_rx_bps: 10 }, { fans_rpm: "2500", net_rx_bps: -1, net_tx_bps: 5 }]) {
    assert.deepEqual(healthView(stats).map((row) => row.value), ["not available", "not available", "not available", "not available"], JSON.stringify(stats));
  }
});
