import assert from "node:assert/strict";
import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { flow } from "./harness.mjs";

const phone = { width: 375, height: 812, deviceScaleFactor: 1, mobile: true };
const lines = [
  "2026-11-20T10:00:00Z Litecoin: 🔴 node down — RPC unreachable for 3 minutes",
  "2026-11-20T10:01:00Z Host: thermal throttle at 90C — capped bitcoin at 1.5 CPUs",
  "2026-11-20T10:01:05Z 🌡 thermal throttle at 90C — capped bitcoin at 1.5 CPUs",
  "2026-11-20T10:02:00Z Host: Monero failed to start (rpcpassword=hunter2); recreated it.",
  "2026-11-20T10:03:00Z Litecoin: 🟢 node recovered — RPC is answering again",
  `2026-11-20T10:04:00Z Settings: Settings changed: ${"low_disk_gib".repeat(12)}.`,
];
const seed = (dash, extra = []) => appendFileSync(join(dash.stateDir, "events.log"), [...lines, ...extra].map((line) => `${line}\n`).join(""));
const rows = (page) => page.evaluate(() => [...document.querySelectorAll("#events-list li")].map((li) => li.textContent.replace(/\s+/g, " ").trim()));
const waitRows = (page, check, description) => page.waitFor(() => document.querySelectorAll("#events-list li").length > 0 && !document.getElementById("events-status").textContent.startsWith("Loading"), { description }).then(async () => {
  const got = await rows(page);
  assert.ok(check(got), `${description}: ${JSON.stringify(got)}`);
  return got;
});

flow("Events panel opens from the keyboard, lists newest first, filters, and closes on Escape", async ({ dash, page }) => {
  seed(dash);
  await page.evaluate(() => document.getElementById("events-open").focus());
  await page.press("Enter");
  await page.waitFor(() => document.getElementById("events-dialog").open, { description: "events dialog to open" });
  assert.equal(await page.evaluate(() => document.getElementById("events-dialog").contains(document.activeElement)), true);
  const all = await waitRows(page, (got) => got.length >= 6, "all events");
  const seeded = all.filter((row) => /Settings changed|node recovered|Details hidden|thermal throttle|node down/.test(row));
  assert.equal(seeded.length, 5, JSON.stringify(all));
  assert.match(seeded[0], /Settings.*Settings changed/);
  assert.match(seeded[1], /Nodes.*node recovered/);
  assert.match(seeded[2], /Nodes.*Error.*Details hidden because they may include a secret\./);
  assert.match(seeded[3], /Host.*Warning.*thermal throttle at 90C/);
  assert.match(seeded[4], /Nodes.*Error.*node down/);
  assert.ok(all.some((row) => /Sign-in and credentials.*Signed in from/.test(row)), "the sign-in is listed");
  assert.equal(all.filter((row) => row.includes("thermal throttle")).length, 1, "the Telegram copy is not listed twice");
  assert.equal(all.some((row) => row.includes("hunter2")), false);

  await page.selectValue("#events-category", "node");
  await waitRows(page, (got) => got.length >= 3 && got.every((row) => row.includes("Nodes")) && got.filter((row) => /node recovered|Details hidden|node down/.test(row)).length === 3, "node events");
  await page.selectValue("#events-node", "litecoin");
  await waitRows(page, (got) => got.length === 2 && got.every((row) => row.includes("Litecoin")), "Litecoin node events");

  await page.press("Escape");
  await page.waitFor(() => !document.getElementById("events-dialog").open, { description: "events dialog to close" });
});

flow("Events panel pages older events and keeps focus in the dialog", async ({ dash, page }) => {
  seed(dash, Array.from({ length: 60 }, (_, i) => `2026-11-21T10:${String(i).padStart(2, "0")}:00Z Wallet: Bitcoin watch-only wallet added.`));
  await page.click("#events-open");
  await waitRows(page, (got) => got.length === 50, "first page");
  await page.waitVisible("#events-more");
  await page.evaluate(() => document.getElementById("events-more").focus());
  await page.press("Enter");
  await page.waitFor(() => document.getElementById("events-more").hidden && document.querySelectorAll("#events-list li").length > 50, { description: "older events" });
  const got = await rows(page);
  assert.equal(new Set(got).size > 1, true);
  assert.equal(await page.evaluate(() => document.activeElement.id), "events-list");
  await page.click("#events-close");
  await page.waitFor(() => !document.getElementById("events-dialog").open, { description: "events dialog to close" });
});

flow("Phone width stacks User, Events and Settings and fits the events dialog", async ({ dash, page }) => {
  seed(dash);
  for (const width of [375, 320]) {
    await page.send("Emulation.setDeviceMetricsOverride", { ...phone, width });
    const buttons = await page.evaluate(() => ["account-open", "events-open", "settings-open"].map((id) => {
      const rect = document.getElementById(id).getBoundingClientRect();
      return { id, top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right };
    }));
    assert.ok(buttons[0].bottom <= buttons[1].top && buttons[1].bottom <= buttons[2].top, `${width}px: ${JSON.stringify(buttons)}`);
    assert.ok(buttons.every((button) => button.left === buttons[0].left && button.right <= width), `${width}px: ${JSON.stringify(buttons)}`);
    await page.click("#events-open");
    await waitRows(page, (got) => got.length >= 6, `${width}px events`);
    const fit = await page.evaluate(() => {
      const dialog = document.getElementById("events-dialog");
      const rect = dialog.getBoundingClientRect();
      const overflow = [...dialog.querySelectorAll("*")].filter((el) => el.getBoundingClientRect().right > rect.right + 0.5).map((el) => el.tagName + (el.id ? `#${el.id}` : ""));
      return { left: rect.left, right: rect.right, overflow, scrollWidth: document.documentElement.scrollWidth, dialogScroll: dialog.scrollWidth - dialog.clientWidth };
    });
    assert.ok(fit.left >= 0 && fit.right <= width, `${width}px: ${JSON.stringify(fit)}`);
    assert.deepEqual(fit.overflow, [], `${width}px`);
    assert.equal(fit.dialogScroll, 0, `${width}px`);
    assert.ok(fit.scrollWidth <= width, `${width}px: scrollWidth ${fit.scrollWidth}`);
    await page.click("#events-close");
    await page.waitFor(() => !document.getElementById("events-dialog").open, { description: "events dialog to close" });
  }
});
