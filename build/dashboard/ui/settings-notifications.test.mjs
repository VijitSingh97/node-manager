import assert from "node:assert/strict";
import { expectSettings, flow, openAlertPreferences, openSettings, reopenSettings, save } from "./harness.mjs";

const GLOBAL = "/api/settings?scope=global";
const lastGlobalBody = (page) => page.requestsTo("POST", GLOBAL).at(-1).body;
const TOKEN = "123456789:ui-flow-token";
const gated = ["#settings-telegram-token", "#settings-telegram-chat", "#settings-node-down-after-minutes", "#settings-low-disk-gib"];
const alerts = [
  ["alert_monitor_online", "#settings-alert-monitor-online"], ["alert_node_down", "#settings-alert-node-down"],
  ["alert_node_recovered", "#settings-alert-node-recovered"], ["alert_sync_complete", "#settings-alert-sync-complete"],
  ["alert_low_disk", "#settings-alert-low-disk"], ["alert_updates", "#settings-alert-updates"],
];
const telegramInputs = [...gated, ...alerts.map(([, selector]) => selector)];

const allDisabled = async (page, selectors, wanted) => {
  for (const selector of selectors) assert.equal(await page.disabled(selector), wanted, `${selector} disabled`);
};

flow("Telegram alerts turned on gate their inputs, save the credentials and show them again", async ({ dash, page }) => {
  await openSettings(page);
  assert.equal(await page.checked("#settings-telegram-enabled"), false);
  await allDisabled(page, telegramInputs, true);

  await page.setChecked("#settings-telegram-enabled", true);
  await allDisabled(page, telegramInputs, false);
  assert.equal(await page.evaluate(() => document.getElementById("settings-telegram-token").required && document.getElementById("settings-telegram-chat").required), true);

  await page.type("#settings-telegram-token", TOKEN);
  await page.type("#settings-telegram-chat", "-10042");
  await save(page, "Settings saved.");
  const body = lastGlobalBody(page);
  assert.equal(body.telegram_enabled, true);
  assert.equal(body.telegram_chat_id, "-10042");
  assert.equal(body.telegram_bot_token, TOKEN);
  expectSettings(dash, { telegram_enabled: true, telegram_bot_token: TOKEN, telegram_chat_id: "-10042" });

  await reopenSettings(page);
  assert.equal(await page.checked("#settings-telegram-enabled"), true);
  assert.equal(await page.value("#settings-telegram-token"), TOKEN);
  assert.equal(await page.value("#settings-telegram-chat"), "-10042");
  await allDisabled(page, telegramInputs, false);
});

flow("Telegram alerts turned off disable their inputs and clear the stored credentials", { settings: { telegram_enabled: true, telegram_bot_token: TOKEN, telegram_chat_id: "-10042" } }, async ({ dash, page }) => {
  await openSettings(page);
  assert.equal(await page.checked("#settings-telegram-enabled"), true);
  assert.equal(await page.value("#settings-telegram-chat"), "-10042");
  await page.setChecked("#settings-telegram-enabled", false);
  await allDisabled(page, telegramInputs, true);
  await save(page, "Settings saved.");
  const body = lastGlobalBody(page);
  assert.equal(body.telegram_enabled, false);
  assert.equal(body.telegram_chat_id, "");
  assert.equal("telegram_bot_token" in body, false);
  expectSettings(dash);

  await reopenSettings(page);
  assert.equal(await page.checked("#settings-telegram-enabled"), false);
  assert.equal(await page.value("#settings-telegram-token"), "");
  assert.equal(await page.value("#settings-telegram-chat"), "");
  await allDisabled(page, telegramInputs, true);
});

flow("Telegram alerts need a token and a chat before Save sends anything", async ({ page }) => {
  await openSettings(page);
  await page.setChecked("#settings-telegram-enabled", true);
  await page.click("#settings-save");
  await page.waitFor(() => document.getElementById("settings-telegram-token").matches(":invalid"));
  assert.equal(page.requestsTo("POST", GLOBAL).length, 0);
  await page.type("#settings-telegram-token", "short");
  await page.type("#settings-telegram-chat", "-1");
  await save(page, "invalid notification settings");
});

for (const [field, selector] of alerts) {
  flow(`Alert preference ${field} flips off and on`, { settings: { telegram_enabled: true, telegram_bot_token: TOKEN, telegram_chat_id: "-10042" } }, async ({ dash, page }) => {
    const base = { telegram_enabled: true, telegram_bot_token: TOKEN, telegram_chat_id: "-10042" };
    await openSettings(page);
    await openAlertPreferences(page);
    assert.equal(await page.checked(selector), true);
    await page.setChecked(selector, false);
    await save(page, "Settings saved.");
    assert.equal(lastGlobalBody(page)[field], false);
    expectSettings(dash, { ...base, [field]: false });
    await reopenSettings(page);
    assert.equal(await page.checked(selector), false);
    for (const [other, otherSelector] of alerts) if (other !== field) assert.equal(await page.checked(otherSelector), true, otherSelector);

    await page.setChecked(selector, true);
    await save(page, "Settings saved.");
    assert.equal(lastGlobalBody(page)[field], true);
    expectSettings(dash, base);
    await reopenSettings(page);
    assert.equal(await page.checked(selector), true);
  });
}

for (const [field, selector, valid, invalid, low, high] of [
  ["node_down_after_minutes", "#settings-node-down-after-minutes", 12, ["0", "61", ""], 1, 60],
  ["low_disk_gib", "#settings-low-disk-gib", 250, ["0", "1048577", ""], 1, 1048576],
]) {
  flow(`${field} accepts its bounds and blocks Save outside them`, { settings: { telegram_enabled: true, telegram_bot_token: TOKEN, telegram_chat_id: "-10042" } }, async ({ dash, page }) => {
    const base = { telegram_enabled: true, telegram_bot_token: TOKEN, telegram_chat_id: "-10042" };
    await openSettings(page);
    await openAlertPreferences(page);
    for (const value of [valid, low, high]) {
      await page.type(selector, String(value));
      await save(page, "Settings saved.");
      assert.equal(lastGlobalBody(page)[field], value);
      expectSettings(dash, { ...base, [field]: value });
      await reopenSettings(page);
      assert.equal(await page.value(selector), String(value));
    }
    const posts = page.requestsTo("POST", GLOBAL).length;
    for (const value of invalid) {
      await page.type(selector, value);
      await page.click("#settings-save");
      await page.waitFor((sel) => document.querySelector(sel).matches(":invalid"), { args: [selector] });
    }
    assert.equal(page.requestsTo("POST", GLOBAL).length, posts);
    expectSettings(dash, { ...base, [field]: high });
  });
}

flow("Healthchecks turned on gate the URL, save it and show it again", async ({ dash, page }) => {
  const url = "https://hc-ping.invalid/ui-flow-check";
  await openSettings(page);
  assert.equal(await page.checked("#settings-healthchecks-enabled"), false);
  assert.equal(await page.disabled("#settings-healthchecks-url"), true);
  await page.setChecked("#settings-healthchecks-enabled", true);
  assert.equal(await page.disabled("#settings-healthchecks-url"), false);
  assert.equal(await page.evaluate(() => document.getElementById("settings-healthchecks-url").required), true);
  await page.type("#settings-healthchecks-url", url);
  await save(page, "Settings saved.");
  assert.equal(lastGlobalBody(page).healthchecks_enabled, true);
  assert.equal(lastGlobalBody(page).healthchecks_url, url);
  expectSettings(dash, { healthchecks_enabled: true, healthchecks_url: url });
  await reopenSettings(page);
  assert.equal(await page.checked("#settings-healthchecks-enabled"), true);
  assert.equal(await page.value("#settings-healthchecks-url"), url);
});

flow("Healthchecks turned off disable the URL and clear it", { settings: { healthchecks_enabled: true, healthchecks_url: "https://hc-ping.invalid/ui-flow-check" } }, async ({ dash, page }) => {
  await openSettings(page);
  assert.equal(await page.checked("#settings-healthchecks-enabled"), true);
  await page.setChecked("#settings-healthchecks-enabled", false);
  assert.equal(await page.disabled("#settings-healthchecks-url"), true);
  await save(page, "Settings saved.");
  assert.equal(lastGlobalBody(page).healthchecks_enabled, false);
  assert.equal("healthchecks_url" in lastGlobalBody(page), false);
  expectSettings(dash);
  await reopenSettings(page);
  assert.equal(await page.checked("#settings-healthchecks-enabled"), false);
  assert.equal(await page.value("#settings-healthchecks-url"), "");
});

flow("Healthchecks shows the server's refusal of a plain http URL", async ({ dash, page }) => {
  await openSettings(page);
  await page.setChecked("#settings-healthchecks-enabled", true);
  await page.type("#settings-healthchecks-url", "http://hc-ping.invalid/insecure");
  await save(page, "invalid notification settings");
  assert.equal(dash.settings(), undefined);
});

const actuatorStatus = async (dash, endpoint, login) => {
  const headers = login ? { authorization: `Basic ${Buffer.from(`${login.username}:${login.password}`).toString("base64")}` } : {};
  return (await fetch(`${dash.url}/actuator/${endpoint}`, { headers, redirect: "manual" })).status;
};

for (const endpoint of ["health", "info"]) {
  const key = `actuator_${endpoint}`;
  const serving = (status) => (endpoint === "health" ? [200, 503].includes(status) : status === 200);
  for (const turnOn of [true, false]) {
    flow(`Actuator ${endpoint} turned ${turnOn ? "on" : "off"}`, { settings: turnOn ? undefined : { [key]: true } }, async ({ dash, page }) => {
      const login = dash.readJSON("actuator-credentials.json");
      assert.equal(login.username, "actuator");
      assert.equal(serving(await actuatorStatus(dash, endpoint, login)), !turnOn);
      await openSettings(page);
      assert.equal(await page.value("#settings-actuator-username"), login.username);
      assert.equal(await page.value("#settings-actuator-password"), login.password);
      assert.equal(await page.checked(`#settings-actuator-${endpoint}`), !turnOn);
      await page.setChecked(`#settings-actuator-${endpoint}`, turnOn);
      await save(page, "Settings saved.");
      assert.equal(lastGlobalBody(page)[key], turnOn);
      expectSettings(dash, { [key]: turnOn });
      const status = await actuatorStatus(dash, endpoint, login);
      assert.equal(serving(status), turnOn);
      assert.equal(turnOn ? await actuatorStatus(dash, endpoint) : status, turnOn ? 401 : 404);
      await reopenSettings(page);
      assert.equal(await page.checked(`#settings-actuator-${endpoint}`), turnOn);
    });
  }
}
