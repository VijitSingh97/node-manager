import assert from "node:assert/strict";
import { expectSettings, flow, openSettings, reload, reopenSettings, save } from "./harness.mjs";

const GLOBAL = "/api/settings?scope=global";
const currencies = ["EUR", "GBP", "INR", "CAD", "AUD", "JPY", "RUB", "CNY", "USD"];

const toggles = [
  {
    label: "Label towers", field: "tower_labels", input: "#settings-tower-labels", hostStats: false,
    effect: async (page) => (await page.evaluate(() => [document.body.dataset.towerLabels, getComputedStyle(document.querySelector(".node-choice-bitcoin .node-choice-label")).display])).join(" "),
    shown: { on: "1 table", off: "0 none" },
  },
  {
    label: "Node manager usage", field: "computer_stats", input: "#settings-computer-stats", hostStats: true,
    effect: async (page) => (await page.evaluate(() => [document.body.dataset.computerStats, getComputedStyle(document.getElementById("host-stats")).display === "none" ? "hidden" : "shown"])).join(" "),
    shown: { on: "1 shown", off: "0 hidden" },
  },
  { label: "Thermal throttle", field: "thermal_throttle", input: "#settings-thermal-throttle", effect: async () => "", shown: { on: "", off: "" } },
];

for (const toggle of toggles) {
  for (const turnOn of [true, false]) {
    flow(`${toggle.label} turned ${turnOn ? "on" : "off"}`, { settings: turnOn ? undefined : { [toggle.field]: true }, hostStats: toggle.hostStats }, async ({ dash, page }) => {
      const before = turnOn ? "off" : "on";
      const after = turnOn ? "on" : "off";
      if (toggle.field === "tower_labels") await page.waitVisible(".node-choice-bitcoin");
      await openSettings(page);
      assert.equal(await page.checked(toggle.input), !turnOn);
      assert.equal(await toggle.effect(page), toggle.shown[before]);

      await page.setChecked(toggle.input, turnOn);
      await save(page, "Settings saved.");
      const [posted] = page.requestsTo("POST", GLOBAL).slice(-1);
      assert.equal(posted.body[toggle.field], turnOn);
      expectSettings(dash, { [toggle.field]: turnOn });
      assert.equal(await toggle.effect(page), toggle.shown[after]);

      await reopenSettings(page);
      assert.equal(await page.checked(toggle.input), turnOn);
      await reload(page, dash);
      assert.equal(await toggle.effect(page), toggle.shown[after]);
      await openSettings(page);
      assert.equal(await page.checked(toggle.input), turnOn);
    });
  }
}

flow("Default currency accepts every option and goes back to the default", async ({ dash, page }) => {
  await page.evaluate(() => document.addEventListener("appearance-updated", (event) => { window.lastAppearance = event.detail; }));
  await openSettings(page);
  assert.equal(await page.value("#settings-default-currency"), "USD");
  assert.deepEqual(await page.evaluate(() => Array.from(document.getElementById("settings-default-currency").options, (option) => option.value)), ["USD", "EUR", "GBP", "INR", "CAD", "AUD", "JPY", "RUB", "CNY"]);
  for (const currency of currencies) {
    await page.selectValue("#settings-default-currency", currency);
    await save(page, "Settings saved.");
    assert.equal(page.requestsTo("POST", GLOBAL).at(-1).body.default_currency, currency);
    expectSettings(dash, { default_currency: currency });
    assert.equal((await page.evaluate(() => window.lastAppearance)).default_currency, currency);
    assert.equal(await page.value("#btc-price-currency"), currency);
    await reopenSettings(page);
    assert.equal(await page.value("#settings-default-currency"), currency);
  }
});
