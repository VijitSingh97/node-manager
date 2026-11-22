import assert from "node:assert/strict";
import { flow } from "./harness.mjs";

flow("Default towers show a focus ring, unreachable towers say so in text and the pies carry a legend", {
  nodes: ["tari"], rpcDown: true, settings: { computer_stats: true },
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
  assert.match(cue, /unreachable/);

  await page.waitFor(() => document.querySelectorAll(".host-stats-pies .pie-legend li").length >= 4, { description: "pie legends" });
  const legends = await page.evaluate(() => [...document.querySelectorAll(".host-stats-pies figure")].map((figure) => figure.querySelector(".pie-legend").textContent));
  assert.equal(legends.length, 2);
  assert.match(legends[0], /Bitcoin.*% CPU/);
  assert.match(legends[0], /Idle/);
  assert.match(legends[1], /Free/);
});
