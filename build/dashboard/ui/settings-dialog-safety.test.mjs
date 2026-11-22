import assert from "node:assert/strict";
import { answerConfirm, flow, nodeIDs, openHostSettings } from "./harness.mjs";

const monero = `#settings-host-nodes .settings-host-node:nth-child(${nodeIDs.indexOf("monero") + 1}) button`;
const settingsOpen = (page) => page.evaluate(() => document.getElementById("settings-dialog").open);

async function escapeUntilClosed(page) {
  for (let presses = 0; presses < 6 && (await settingsOpen(page)); presses++) {
    await page.press("Escape");
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  assert.equal(await settingsOpen(page), false);
}

async function stageInstall(page) {
  await openHostSettings(page);
  await page.click(monero);
  await page.click("#settings-install-submit");
  await page.waitText(monero, "Installs on Save");
}

for (const [name, close] of [["Cancel", (page) => page.click("#settings-cancel")], ["the X button", (page) => page.click("#settings-close")], ["Escape", (page) => page.press("Escape")]]) {
  flow(`Closing Settings with ${name} asks before discarding a staged install`, async ({ page, helper }) => {
    await stageInstall(page);
    await close(page);
    await answerConfirm(page, false);
    assert.equal(await settingsOpen(page), true);
    assert.equal(await page.text(monero), "Installs on Save");
    await close(page);
    await answerConfirm(page, true);
    await page.waitFor(() => !document.getElementById("settings-dialog").open, { description: "settings dialog to close" });
    assert.equal(helper.requests.length, 0);
  });
}

flow("A forced close keeps the staged install and reopening shows it still staged", async ({ page }) => {
  await stageInstall(page);
  await escapeUntilClosed(page);
  await page.click("#settings-open");
  await page.waitFor(() => document.getElementById("settings-dialog").open, { description: "settings dialog to reopen" });
  await page.waitText(monero, "Installs on Save");
});

flow("Cancel after Reset to default asks, and a forced close keeps the reset staged", { settings: { low_disk_gib: 77 } }, async ({ page }) => {
  await openHostSettings(page);
  await page.click("#settings-reset");
  const lowDisk = await page.evaluate(() => document.getElementById("settings-low-disk-gib").value);
  await page.click("#settings-cancel");
  await answerConfirm(page, false);
  assert.equal(await settingsOpen(page), true);
  await escapeUntilClosed(page);
  await page.click("#settings-open");
  await page.waitFor((value) => document.getElementById("settings-dialog").open && document.getElementById("settings-low-disk-gib").value === value, { args: [lowDisk], description: "reset values to stay filled in" });
});

flow("The confirm dialog is described by its message", async ({ page }) => {
  assert.equal(await page.evaluate(() => document.getElementById("confirm-dialog").getAttribute("aria-describedby")), "confirm-dialog-message");
});

flow("Closing Settings with only a staged Run new config asks before discarding it", { capability: { config_pending: true, config_revision: "ab12".repeat(16) } }, async ({ page }) => {
  await openHostSettings(page);
  await page.click("#settings-apply-config");
  await page.click("#settings-cancel");
  await page.waitFor(() => document.getElementById("confirm-dialog").open, { description: "confirm dialog to open" });
  assert.equal(await page.text("#confirm-dialog-message"), "Discard the staged changes?");
  await answerConfirm(page, true);
  await page.waitFor(() => !document.getElementById("settings-dialog").open, { description: "settings dialog to close" });
});

flow("Closing Settings with nothing staged asks nothing", async ({ page }) => {
  await openHostSettings(page);
  await page.press("Escape");
  await page.waitFor(() => !document.getElementById("settings-dialog").open, { description: "settings dialog to close" });
  assert.equal(await page.evaluate(() => document.getElementById("confirm-dialog").open), false);
});

flow("A node's new-block switch says nothing is sent while Telegram is off", async ({ page }) => {
  await page.click('[data-select-node="bitcoin"]');
  await page.openDetails("#settings-bitcoin-alerts");
  await page.waitVisible("#settings-bitcoin-alerts [data-telegram-off-note]");
  assert.match(await page.text("#settings-bitcoin-alerts [data-telegram-off-note]"), /Telegram is off, so no alerts are sent/);
});

flow("The note is hidden while Telegram is on", { settings: { telegram_enabled: true, telegram_bot_token: "123456789:ui-flow-token", telegram_chat_id: "-10042" } }, async ({ page }) => {
  await page.click('[data-select-node="bitcoin"]');
  await page.openDetails("#settings-bitcoin-alerts");
  await page.waitFor(() => document.getElementById("settings-bitcoin-new-block") && !document.getElementById("settings-bitcoin-new-block").disabled, { description: "alerts to load" });
  assert.equal(await page.visible("#settings-bitcoin-alerts [data-telegram-off-note]"), false);
});

flow("Escape in the account dialog closes only that dialog", async ({ page }) => {
  await page.click('[data-select-node="bitcoin"]');
  await page.waitVisible("[data-node-power=bitcoin]");
  await page.click("#account-open");
  await page.waitFor(() => document.getElementById("account-dialog").open);
  await page.press("Escape");
  await page.waitFor(() => !document.getElementById("account-dialog").open);
  assert.equal(await page.evaluate(() => document.body.dataset.node), "bitcoin");
});

flow("Escape in the node power dialog closes only that dialog", async ({ page }) => {
  await page.click('[data-select-node="bitcoin"]');
  await page.waitFor(() => { const b = document.querySelector("[data-node-power=bitcoin]"); return b && !b.disabled; });
  await page.click("[data-node-power=bitcoin]");
  await page.waitFor(() => document.getElementById("node-power-dialog").open);
  await page.press("Escape");
  await page.waitFor(() => !document.getElementById("node-power-dialog").open);
  assert.equal(await page.evaluate(() => document.body.dataset.node), "bitcoin");
});
