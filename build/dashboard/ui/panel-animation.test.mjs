import { flow, nodeIDs } from "./harness.mjs";

flow("Closing a panel works while its open animation is stalled", { nodes: nodeIDs.filter((id) => id !== "bitcoin") }, async ({ page }) => {
  await page.click('[data-select-node="monero"]');
  await page.evaluate(() => {
    document.getAnimations().forEach((animation) => animation.updatePlaybackRate(0.05));
    addEventListener("mousemove", () => document.getAnimations().forEach((animation) => animation.finish()), { once: true, capture: true });
  });
  await page.click('[data-node-panel="monero"] [data-close-node]');
  await page.waitFor((id) => document.querySelector(`[data-node-panel="${id}"]`).hidden, { args: ["monero"] });
});
