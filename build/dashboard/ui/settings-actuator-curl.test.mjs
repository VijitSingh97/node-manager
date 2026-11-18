import assert from "node:assert/strict";
import { flow, openSettings, reopenSettings, save } from "./harness.mjs";

const COPY = { health: "#settings-actuator-copy-health", info: "#settings-actuator-copy-info" };
const PASSWORD = "ui-flows-password";
const ON = { settings: { actuator_health: true, actuator_info: true } };

const stubClipboard = (page, mode) => page.evaluate((how) => {
  window.__copied = [];
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: how === "api" ? { writeText: async (text) => { window.__copied.push(text); } } : undefined });
  document.execCommand = (command) => {
    const area = [...document.querySelectorAll("textarea[aria-hidden]")].at(-1);
    if (command !== "copy" || !area || how === "none") return false;
    window.__copied.push(area.value);
    return true;
  };
}, mode);

const copyFor = async (page, endpoint) => {
  await page.click(COPY[endpoint]);
  await page.waitVisible("#actuator-curl-password");
  await page.type("#actuator-curl-password", PASSWORD);
  await page.click("#actuator-curl-submit");
};

flow("Copy curl buttons stay hidden until the switches are saved on", async ({ page }) => {
  await openSettings(page);
  for (const selector of Object.values(COPY)) assert.equal(await page.visible(selector), false);
  assert.equal(await page.visible("#settings-actuator-copy-note"), true);
  await page.setChecked("#settings-actuator-health", true);
  assert.equal(await page.visible(COPY.health), false);
  await save(page, "Settings saved.");
  assert.equal(await page.visible(COPY.health), true);
  assert.equal(await page.visible(COPY.info), false);
  assert.equal(await page.visible("#settings-actuator-copy-note"), false);
  await page.setChecked("#settings-actuator-health", false);
  await save(page, "Settings saved.");
  for (const selector of Object.values(COPY)) assert.equal(await page.visible(selector), false);
});

for (const endpoint of ["health", "info"]) {
  flow(`Copy curl for ${endpoint} copies the full command over plain http without the clipboard API`, ON, async ({ dash, page }) => {
    await openSettings(page);
    await stubClipboard(page, "exec");
    await copyFor(page, endpoint);
    await page.waitText(COPY[endpoint], "Copied");
    assert.deepEqual(await page.evaluate(() => window.__copied), [`curl --user 'admin:${PASSWORD}' '${dash.url}/actuator/${endpoint}'`]);
    assert.equal(await page.evaluate(() => document.getElementById("actuator-curl-dialog").open), false);
    assert.equal(await page.value("#actuator-curl-password"), "");
    assert.equal(await page.evaluate(() => document.querySelectorAll("textarea[aria-hidden]").length), 0);
    assert.equal(page.requestsTo("POST", "/api/settings/actuator-curl").length, 1);
  });
}

flow("Copy curl uses navigator.clipboard when the page has it", ON, async ({ dash, page }) => {
  await openSettings(page);
  await stubClipboard(page, "api");
  await copyFor(page, "health");
  await page.waitText(COPY.health, "Copied");
  assert.deepEqual(await page.evaluate(() => window.__copied), [`curl --user 'admin:${PASSWORD}' '${dash.url}/actuator/health'`]);
});

flow("Copy curl shows a selected read-only field when copying fails and clears it on close", ON, async ({ dash, page }) => {
  await openSettings(page);
  await stubClipboard(page, "none");
  await copyFor(page, "info");
  await page.waitText(COPY.info, "Copy failed - select and copy");
  await page.waitVisible("#actuator-curl-fallback");
  assert.equal(await page.value("#actuator-curl-fallback"), `curl --user 'admin:${PASSWORD}' '${dash.url}/actuator/info'`);
  assert.equal(await page.text("#actuator-curl-fallback-label"), "Press Ctrl+C / Cmd+C to copy");
  assert.equal(await page.evaluate(() => { const field = document.getElementById("actuator-curl-fallback"); return field.readOnly && field.selectionStart === 0 && field.selectionEnd === field.value.length; }), true);
  assert.equal(await page.value("#actuator-curl-password"), "");
  assert.equal(await page.text("#actuator-curl-submit"), "Copy again");
  await stubClipboard(page, "exec");
  await page.click("#actuator-curl-submit");
  await page.waitText(COPY.info, "Copied");
  assert.deepEqual(await page.evaluate(() => window.__copied), [`curl --user 'admin:${PASSWORD}' '${dash.url}/actuator/info'`]);
  assert.equal(await page.evaluate(() => document.getElementById("actuator-curl-dialog").open), false);
  await page.click(COPY.info);
  await page.waitVisible("#actuator-curl-password");
  await stubClipboard(page, "none");
  await page.type("#actuator-curl-password", PASSWORD);
  await page.click("#actuator-curl-submit");
  await page.waitVisible("#actuator-curl-fallback");
  await page.click("#actuator-curl-cancel");
  assert.equal(await page.value("#actuator-curl-fallback"), "");
  assert.equal(await page.value("#actuator-curl-password"), "");
  assert.equal(await page.evaluate(() => document.documentElement.outerHTML.includes("ui-flows-password")), false);
  await reopenSettings(page);
  await page.waitText(COPY.info, "Copy curl for info");
});
