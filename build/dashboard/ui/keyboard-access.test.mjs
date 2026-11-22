import assert from "node:assert/strict";
import { flow } from "./harness.mjs";

const focused = (page) => page.evaluate(() => document.activeElement?.getAttribute("data-zoom") || document.activeElement?.className || "");

flow("The shortened wallet address is a button that Enter and Space expand and collapse", async ({ page }) => {
  await page.evaluate(async () => {
    const { managerRow, replaceIfChanged } = await import("/static/wallet-ui.js");
    const list = document.createElement("div");
    list.id = "kb-list";
    document.body.prepend(list);
    replaceIfChanged(list, "k", () => [managerRow({ label: "Main", keyText: `bc1q${"x".repeat(60)}`, canRemove: true, onRemove() {} })]);
    document.querySelector("#kb-list .wk").focus();
  });
  const state = () => page.evaluate(() => {
    const key = document.querySelector("#kb-list .wk");
    return [key.tagName, key.getAttribute("aria-expanded"), key.textContent.length > 40, document.activeElement === key];
  });
  assert.deepEqual(await state(), ["BUTTON", "false", false, true]);
  await page.press("Enter");
  assert.deepEqual(await state(), ["BUTTON", "true", true, true]);
  await page.press("Space");
  assert.deepEqual(await state(), ["BUTTON", "false", false, true]);
  await page.evaluate(async () => {
    const { replaceIfChanged, managerRow } = await import("/static/wallet-ui.js");
    const list = document.getElementById("kb-list");
    replaceIfChanged(list, "k2", () => [managerRow({ label: "Main", keyText: `bc1q${"x".repeat(60)}`, canRemove: true, onRemove() {} })]);
  });
  assert.equal((await state())[3], true);
  await page.press("Enter");
  assert.deepEqual(await state(), ["BUTTON", "true", true, true]);
  await page.evaluate(async () => {
    const { replaceIfChanged, managerRow } = await import("/static/wallet-ui.js");
    replaceIfChanged(document.getElementById("kb-list"), "k3", () => [managerRow({ label: "Main", keyText: `bc1q${"x".repeat(60)}`, canRemove: false, onRemove() {} })]);
  });
  assert.deepEqual(await state(), ["BUTTON", "true", true, true]);
});

flow("Host pies and disk bars are keyboard buttons that zoom and keep focus", { settings: { computer_stats: true }, hostStats: true }, async ({ dash, page }) => {
  await page.waitVisible("#host-stats [data-zoom=cpu]");
  const pie = () => page.evaluate(() => {
    const node = document.querySelector("#host-stats [data-zoom=cpu]");
    return [node.getAttribute("role"), node.tabIndex, node.getAttribute("aria-pressed"), node.getAttribute("aria-label"), document.activeElement === node];
  });
  assert.deepEqual((await pie()).slice(0, 3), ["button", 0, "false"]);
  assert.match((await pie())[3], /CPU.*zoom/);
  await page.evaluate(() => document.querySelector("#host-stats [data-zoom=cpu]").focus());
  await page.press("Enter");
  const zoomed = await pie();
  assert.equal(zoomed[2], "true");
  assert.equal(zoomed[4], true);
  await page.press("Space");
  assert.equal((await pie())[2], "false");
  await page.evaluate(() => window.dispatchEvent(new CustomEvent("host-stats-data", { detail: { disk_available: true, disk_total_bytes: 1000, disk_free_bytes: 400, stats: { cpus: 4, mem_total_bytes: 2 ** 33, nodes: {}, other: { cpu_pct: 1, mem_bytes: 1 } } } })));
  const bar = () => page.evaluate(() => {
    const node = document.querySelector("#host-stats [data-zoom=disk-0]");
    return [node.getAttribute("role"), node.getAttribute("aria-pressed"), node.getAttribute("aria-label").length > 10, document.activeElement === node];
  });
  assert.deepEqual(await bar(), ["button", "false", true, false]);
  await page.evaluate(() => document.querySelector("#host-stats [data-zoom=disk-0]").focus());
  await page.press("Enter");
  assert.deepEqual(await bar(), ["button", "true", true, true]);
  await page.evaluate(() => document.querySelector("#host-stats [data-zoom=cpu]").focus());
  await page.evaluate(() => window.dispatchEvent(new CustomEvent("host-stats-data", { detail: { disk_available: true, disk_total_bytes: 1000, disk_free_bytes: 400, stats: { cpus: 4, mem_total_bytes: 2 ** 33, nodes: {}, other: { cpu_pct: 1, mem_bytes: 1 } } } })));
  assert.equal(await page.evaluate(() => document.activeElement?.dataset?.zoom), "cpu");
  const panelTop = () => page.evaluate(() => {
    const panel = document.getElementById("host-stats");
    panel.style.maxHeight = "120px";
    panel.scrollTop = 0;
    window.dispatchEvent(new CustomEvent("host-stats-data", { detail: { disk_available: true, disk_total_bytes: 1000, disk_free_bytes: 400, stats: { cpus: 4, mem_total_bytes: 2 ** 33, nodes: {}, other: { cpu_pct: 1, mem_bytes: 1 } } } }));
    return [panel.scrollHeight > panel.clientHeight, panel.scrollTop, document.activeElement?.dataset?.zoom];
  });
  assert.deepEqual(await panelTop(), [true, 0, "cpu"]);
});

flow("The theme toggles announce the current mode", async ({ page }) => {
  const names = () => page.evaluate(() => Array.from(document.querySelectorAll(".theme-toggle"), (b) => b.getAttribute("aria-label")));
  assert.deepEqual(new Set(await names()), new Set(["Switch theme, now Auto"]));
  await page.click(".theme-toggle");
  assert.deepEqual(new Set(await names()), new Set(["Switch theme, now Light"]));
  await page.click(".theme-toggle");
  assert.deepEqual(new Set(await names()), new Set(["Switch theme, now Dark"]));
});

flow("The chain sections are not live regions", async ({ page }) => {
  assert.deepEqual(await page.evaluate(() => Array.from(document.querySelectorAll("section.card[id$=-live]"), (s) => s.hasAttribute("aria-live"))).then((l) => [l.length > 0, l.some(Boolean)]), [true, false]);
});
