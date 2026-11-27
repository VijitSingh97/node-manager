import assert from "node:assert/strict";
import { answerConfirm, defaultSettings, expectSettings, flow, openAlertPreferences, openSettings, reopenSettings, save } from "./harness.mjs";

const GLOBAL = "/api/settings?scope=global";
const posts = (page) => page.requestsTo("POST", GLOBAL);
const TOKEN = "123456789:ui-flow-token";
const customised = {
  telegram_enabled: true, telegram_bot_token: TOKEN, telegram_chat_id: "-10042", healthchecks_enabled: true, healthchecks_url: "https://hc-ping.invalid/ui-flow-check",
  alert_new_block: true, alert_monitor_online: false, alert_node_down: false, alert_node_recovered: false, alert_sync_complete: false, alert_low_disk: false,
  alert_updates: false, node_down_after_minutes: 7, low_disk_gib: 9, tower_labels: true, computer_stats: true, thermal_throttle: true, default_currency: "EUR", actuator_health: true, actuator_info: true,
};

const readFields = (page) => page.evaluate(() => {
  const get = (id) => document.getElementById(`settings-${id}`);
  return {
    telegram_enabled: get("telegram-enabled").checked, telegram_chat_id: get("telegram-chat").value, healthchecks_enabled: get("healthchecks-enabled").checked,
    healthchecks_url: get("healthchecks-url").value, alert_monitor_online: get("alert-monitor-online").checked, alert_node_down: get("alert-node-down").checked,
    alert_node_recovered: get("alert-node-recovered").checked, alert_sync_complete: get("alert-sync-complete").checked, alert_low_disk: get("alert-low-disk").checked,
    alert_updates: get("alert-updates").checked, node_down_after_minutes: Number(get("node-down-after-minutes").value), low_disk_gib: Number(get("low-disk-gib").value),
    tower_labels: get("tower-labels").checked, computer_stats: get("computer-stats").checked, thermal_throttle: get("thermal-throttle").checked,
    default_currency: get("default-currency").value, actuator_health: get("actuator-health").checked, actuator_info: get("actuator-info").checked,
  };
});

const pick = (settings, fields) => Object.fromEntries(Object.keys(fields).map((key) => [key, settings[key]]));

flow("Save with nothing edited says there are no changes and sends nothing", async ({ dash, page }) => {
  await openSettings(page);
  await save(page, "No changes.");
  assert.equal(posts(page).length, 0);
  assert.equal(dash.settings(), undefined);
});

for (const [name, close] of [
  ["Cancel", (page) => page.click("#settings-cancel")],
  ["the X button", (page) => page.click("#settings-close")],
  ["Escape", (page) => page.press("Escape")],
]) {
  flow(`Closing the Settings dialog with ${name} discards unsaved edits`, { settings: { tower_labels: true } }, async ({ dash, page }) => {
    await openSettings(page);
    await page.setChecked("#settings-tower-labels", false);
    await page.setChecked("#settings-thermal-throttle", true);
    await page.selectValue("#settings-default-currency", "JPY");
    await page.setChecked("#settings-telegram-enabled", true);
    await page.type("#settings-telegram-token", TOKEN);
    await page.type("#settings-telegram-chat", "-1");
    await close(page);
    await page.waitFor(() => !document.getElementById("settings-dialog").open);
    assert.equal(posts(page).length, 0);
    expectSettings(dash, { tower_labels: true });
    assert.equal(await page.evaluate(() => document.body.dataset.towerLabels), "1");
    await openSettings(page);
    assert.equal(await page.checked("#settings-tower-labels"), true);
    assert.equal(await page.checked("#settings-thermal-throttle"), false);
    assert.equal(await page.value("#settings-default-currency"), "USD");
    assert.equal(await page.checked("#settings-telegram-enabled"), false);
    assert.equal(await page.value("#settings-telegram-token"), "");
    assert.equal(await page.value("#settings-telegram-chat"), "");
  });
}

flow("Reset to default fills in the defaults and Cancel keeps the saved settings", { settings: customised }, async ({ dash, page }) => {
  await openSettings(page);
  await openAlertPreferences(page);
  assert.deepEqual(await readFields(page), pick(customised, await readFields(page)));
  await page.click("#settings-reset");
  await page.waitText("#settings-status", "Defaults filled in. Save applies them; Cancel keeps your current settings.");
  assert.deepEqual(await readFields(page), pick(defaultSettings, await readFields(page)));
  assert.equal(posts(page).length, 0);
  assert.deepEqual(dash.settings(), { ...defaultSettings, ...customised });

  await page.click("#settings-cancel");
  await answerConfirm(page, true);
  await page.waitFor(() => !document.getElementById("settings-dialog").open);
  await openSettings(page);
  assert.deepEqual(await readFields(page), pick(customised, await readFields(page)));
  assert.deepEqual(dash.settings(), { ...defaultSettings, ...customised });
});

flow("Reset to default then Save restores every setting", { settings: customised }, async ({ dash, page }) => {
  await openSettings(page);
  await page.click("#settings-reset");
  await save(page, "Defaults restored.");
  assert.deepEqual(posts(page).map((request) => request.body), [{ reset: true }]);
  assert.equal(dash.settings(), undefined);
  assert.equal(await page.evaluate(() => document.body.dataset.towerLabels), "0");
  await openAlertPreferences(page);
  await reopenSettings(page);
  assert.deepEqual(await readFields(page), pick(defaultSettings, await readFields(page)));
});

flow("Reset to default followed by an edit sends the reset and then the edit", { settings: customised }, async ({ dash, page }) => {
  await openSettings(page);
  await page.click("#settings-reset");
  await page.setChecked("#settings-thermal-throttle", true);
  await save(page, "Settings saved.");
  const bodies = posts(page).map((request) => request.body);
  assert.deepEqual(bodies[0], { reset: true });
  assert.equal(bodies[1].thermal_throttle, true);
  assert.equal(bodies.length, 2);
  expectSettings(dash, { thermal_throttle: true });
});
