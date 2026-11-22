import assert from "node:assert/strict";
import { flow } from "./harness.mjs";

flow("Default towers show a focus ring, unreachable towers say so in text and the pies carry a legend", {
  nodes: ["monero", "tari"], rpcDown: true, settings: { computer_stats: true },
  hostStats: { host_cpu_pct: 20, host_mem_bytes: 2 ** 30, nodes: { bitcoin: { cpu_pct: 5, mem_bytes: 2 ** 29 } } },
}, async ({ page }) => {
  await page.waitFor(() => document.querySelector('[data-select-node="bitcoin"]')?.dataset.nodeUnreachable === "1", { description: "bitcoin unreachable", timeout: 45000 });

  await page.press("Escape");
  const focus = await page.evaluate(() => {
    const choice = document.querySelector('[data-select-node="tari"]');
    choice.focus();
    const label = choice.querySelector("span");
    const style = getComputedStyle(label);
    return { focused: document.activeElement === choice, shown: style.display !== "none", ring: style.boxShadow };
  });
  assert.equal(focus.focused, true);
  assert.equal(focus.shown, true, "focusing a default tower reveals its name");
  assert.notEqual(focus.ring, "none");
  assert.match(focus.ring, /0px 0px 0px 2px/);

  const cue = await page.evaluate(() => getComputedStyle(document.querySelector('[data-select-node="bitcoin"] .node-choice-label b'), "::after").content);
  assert.match(cue, /down/);
  const name = await page.evaluate(() => document.querySelector('[data-select-node="bitcoin"]').getAttribute("aria-label"));
  assert.match(name, /^Bitcoin.*not responding/);
  const fresh = await page.evaluate(() => {
    const live = document.getElementById("live");
    live.dataset.nextBlock = "101";
    window.dispatchEvent(new Event("live-updated"));
    const label = document.querySelector('[data-select-node="bitcoin"]').getAttribute("aria-label");
    delete live.dataset.nextBlock;
    window.dispatchEvent(new Event("live-updated"));
    return { label, after: document.querySelector('[data-select-node="bitcoin"]').getAttribute("aria-label") };
  });
  assert.doesNotMatch(fresh.label, /not responding/, "a fresh Bitcoin height clears the spoken cue like the visual one");
  assert.match(fresh.after, /not responding/);

  await page.send("Emulation.setDeviceMetricsOverride", { width: 360, height: 740, deviceScaleFactor: 1, mobile: false });
  await page.goto(page.origin + "/");
  await page.waitFor(() => document.querySelector('[data-select-node="tari"]')?.dataset.nodeUnreachable === "1", { description: "tari unreachable after reload", timeout: 45000 });
  await page.waitFor(() => innerWidth === 360 && document.querySelector('[data-select-node="tari"] span').getBoundingClientRect().right <= 360, { description: "phone layout", timeout: 8000 });
  const boxes = await page.evaluate(() => ["bitcoin", "monero", "tari"].map((id) => { const r = document.querySelector(`[data-select-node="${id}"] span`).getBoundingClientRect(); return { id, left: r.left, right: r.right, top: r.top, bottom: r.bottom }; }));
  for (const { id, left, right } of boxes) assert.ok(left >= 0 && right <= 360, `${id} label ${left}-${right} fits 360px`);
  for (const { id, left, right } of boxes) assert.ok(right - left <= 135, `${id} label stays no wider than its price line`);

  await page.waitFor(() => document.querySelectorAll(".host-stats-pies .pie-legend li").length >= 4, { description: "pie legends" });
  const legends = await page.evaluate(() => [...document.querySelectorAll(".host-stats-pies figure")].map((figure) => figure.querySelector(".pie-legend").textContent));
  assert.equal(legends.length, 2);
  assert.match(legends[0], /Bitcoin.*% CPU/);
  assert.match(legends[0], /Idle/);
  assert.match(legends[1], /Free/);
  const swatches = await page.evaluate(() => [...document.querySelectorAll(".pie-legend i")].map((i) => getComputedStyle(i).borderTopWidth));
  assert.ok(swatches.length >= 4 && swatches.every((width) => width !== "0px"), "legend swatches carry a border");
});
