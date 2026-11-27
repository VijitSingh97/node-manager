import assert from "node:assert/strict";
import { flow } from "./harness.mjs";

flow("The footer Updated time is shown in the browser time zone on load and after a live refresh", async ({ page }) => {
  await page.send("Emulation.setTimezoneOverride", { timezoneId: "Pacific/Kiritimati" });
  await page.send("Page.reload");
  await page.waitFor(() => document.readyState === "complete" && document.querySelector("time[data-updated-at]"), { description: "footer" });
  const read = () => page.evaluate(() => {
    const node = document.querySelector("time[data-updated-at]");
    const local = new Intl.DateTimeFormat(undefined, { timeStyle: "medium", timeZone: "Pacific/Kiritimati" }).format(new Date(Number(node.dataset.updatedAt) * 1000));
    return { shown: node.textContent, local };
  });
  const loaded = await read();
  assert.equal(loaded.shown, loaded.local);

  await page.evaluate(() => {
    document.querySelector("time[data-updated-at]").textContent = "00:00:00";
    window.dispatchEvent(new Event("live-updated"));
  });
  const live = await read();
  assert.equal(live.shown, live.local);
});
