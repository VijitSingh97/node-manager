import assert from "node:assert/strict";
import { expectSettings, flow, nodeIDs } from "./harness.mjs";

const NODES = "/api/settings?scope=nodes";
const field = (node) => (node === "bitcoin" ? "alert_new_block" : `alert_new_block_${node}`);

async function openPanel(page, node) {
  await page.click(`[data-select-node="${node}"]`);
  await page.waitVisible(`#settings-${node}-alerts > summary`);
  await page.openDetails(`#settings-${node}-alerts`);
  await page.waitVisible(`#settings-${node}-new-block`);
  await page.waitFor((id) => !document.getElementById(`settings-${id}-new-block`).disabled, { args: [node], description: `${node} alert checkbox to be enabled` });
}

async function closePanel(page, node) {
  await page.click(`[data-node-panel="${node}"] [data-close-node]`);
  await page.waitFor((id) => document.querySelector(`[data-node-panel="${id}"]`).hidden, { args: [node] });
}

for (const turnOn of [true, false]) {
  flow(`New-block alerts turned ${turnOn ? "on" : "off"} for every node`, { nodes: nodeIDs.filter((id) => id !== "bitcoin"), settings: turnOn ? undefined : Object.fromEntries(nodeIDs.map((id) => [field(id), true])) }, async ({ dash, page }) => {
    const expected = turnOn ? {} : Object.fromEntries(nodeIDs.map((id) => [field(id), true]));
    for (const node of nodeIDs) {
      await page.waitVisible(`[data-select-node="${node}"]`);
      await openPanel(page, node);
      const input = `#settings-${node}-new-block`;
      assert.equal(await page.checked(input), !turnOn, `${node} starts ${turnOn ? "off" : "on"}`);
      await page.setChecked(input, turnOn);
      await page.click(`form[data-node-settings-form="${node}"] button[type=submit]`);
      await page.waitText(`form[data-node-settings-form="${node}"] > p[role=status]`, turnOn ? "Settings saved. Telegram is off, so no alerts are sent." : "Settings saved.");
      assert.deepEqual(page.requestsTo("POST", NODES).at(-1).body, { [field(node)]: turnOn });
      expected[field(node)] = turnOn;
      expectSettings(dash, expected);
      await closePanel(page, node);
      await openPanel(page, node);
      assert.equal(await page.checked(input), turnOn, `${node} keeps ${turnOn ? "on" : "off"} after reopening`);
      await closePanel(page, node);
    }
  });
}
