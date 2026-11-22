import assert from "node:assert/strict";
import { flow, nodeIDs } from "./harness.mjs";

const nodes = nodeIDs.filter((id) => id !== "bitcoin");
const phone = { width: 375, height: 812, deviceScaleFactor: 1, mobile: true };
const desktop = { width: 1280, height: 1000, deviceScaleFactor: 1, mobile: false };
const settle = (page) => page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 600)));

flow("Phone panels keep the close button and values clear of the fixed buttons", { nodes: nodeIDs }, async ({ page }) => {
  for (const width of [375, 320]) {
    await page.send("Emulation.setDeviceMetricsOverride", { ...phone, width });
    for (const id of nodeIDs) {
      await page.click(`[data-select-node="${id}"]`);
      await settle(page);
      const got = await page.evaluate((id) => {
        const rect = (el) => el.getBoundingClientRect();
        const panel = document.querySelector(`[data-node-panel="${id}"]`);
        const close = panel.querySelector("[data-close-node]");
        const c = rect(close);
        const top = document.elementFromPoint(c.left + c.width / 2, c.top + c.height / 2);
        const toggle = rect(document.getElementById("theme-toggle"));
        const buttons = [...document.querySelectorAll(".settings-open")].map(rect);
        const right = Math.max(...[...panel.querySelectorAll("*")].filter((el) => el !== close && rect(el).width && !el.closest("[hidden]")).map((el) => rect(el).right));
        return { closeHit: top === close, overlapsToggle: !(c.right <= toggle.left || c.left >= toggle.right || c.bottom <= toggle.top || c.top >= toggle.bottom), right, buttonsLeft: Math.min(...buttons.map((b) => b.left)), buttons: buttons.length, scrollWidth: document.documentElement.scrollWidth };
      }, id);
      const where = `${id} at ${width}px`;
      assert.equal(got.closeHit, true, where);
      assert.equal(got.overlapsToggle, false, where);
      assert.equal(got.buttons, 2, where);
      assert.ok(got.right <= got.buttonsLeft, `${where}: panel content reaches ${got.right}px, buttons start at ${got.buttonsLeft}px`);
      assert.ok(got.scrollWidth <= width, `${where}: scrollWidth ${got.scrollWidth}`);
      await page.click(`[data-node-panel="${id}"] [data-close-node]`);
      await settle(page);
    }
  }
});

flow("Phone host stats leave the towers clickable", { nodes, hostStats: true, settings: { computer_stats: true } }, async ({ page }) => {
  await page.send("Emulation.setDeviceMetricsOverride", phone);
  await page.waitVisible("#host-stats");
  await settle(page);
  const got = await page.evaluate(() => {
    const stats = document.getElementById("host-stats").getBoundingClientRect();
    const toggle = document.getElementById("theme-toggle").getBoundingClientRect();
    const hit = (x, y) => document.elementFromPoint(x, y);
    const below = hit(innerWidth / 2, stats.bottom + 8);
    const lower = [0.2, 0.4, 0.6, 0.8].map((f) => hit(innerWidth * f, innerHeight * 0.85));
    return {
      statsBottom: stats.bottom, height: innerHeight, coversToggle: stats.top < toggle.bottom,
      belowInStats: !!below.closest("#host-stats"), towerHits: lower.filter((el) => el.closest(".node-choice")).length,
    };
  });
  assert.ok(got.statsBottom <= got.height * 0.4, `host stats reach ${got.statsBottom}px of ${got.height}px`);
  assert.equal(got.coversToggle, false);
  assert.equal(got.belowInStats, false);
  assert.ok(got.towerHits >= 1, "a tower must be clickable under the host stats");
});

flow("Desktop add-address placeholders are fully readable", { nodes }, async ({ page }) => {
  await page.send("Emulation.setDeviceMetricsOverride", desktop);
  await page.click('[data-select-node="litecoin"]');
  await page.waitVisible(".family-watch-form input");
  const got = await page.evaluate(() => [...document.querySelectorAll('[data-node-panel="litecoin"] .family-watch-form input')].map((input) => {
    const style = getComputedStyle(input);
    const context = document.createElement("canvas").getContext("2d");
    context.font = style.font;
    const room = input.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
    return { placeholder: input.placeholder, need: context.measureText(input.placeholder).width, room };
  }));
  assert.equal(got.length, 2);
  for (const field of got) assert.ok(field.need <= field.room, `${field.placeholder} needs ${field.need}px, has ${field.room}px`);
});

flow("Desktop panel close button is not covered", { nodes }, async ({ page }) => {
  await page.send("Emulation.setDeviceMetricsOverride", desktop);
  await page.click('[data-select-node="litecoin"]');
  await settle(page);
  const hit = await page.evaluate(() => {
    const close = document.querySelector('[data-node-panel="litecoin"] [data-close-node]');
    const c = close.getBoundingClientRect();
    return document.elementFromPoint(c.left + c.width / 2, c.top + c.height / 2) === close;
  });
  assert.equal(hit, true);
});
