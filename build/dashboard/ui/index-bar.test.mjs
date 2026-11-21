import assert from "node:assert/strict";
import { flow, nodeIDs, openHostSettings, reload, save, waitSaved, withoutIdentity } from "./harness.mjs";

const row = (node) => `#settings-host-nodes .settings-host-node:nth-child(${nodeIDs.indexOf(node) + 1})`;
const rowButton = (node) => `${row(node)} button`;

flow("The install dialog offers the balance index ticked by default for a chain that has one and sends the choice", { capability: { install_index_supported: true } }, async ({ page, helper }) => {
  await openHostSettings(page);
  await page.click(rowButton("dogecoin"));
  await page.waitVisible("#settings-install-index-label");
  assert.equal(await page.checked("#settings-install-index"), true);
  await page.click("#settings-install-submit");
  await page.click(rowButton("bitcoincash"));
  await page.waitVisible("#settings-install-index-label");
  assert.equal(await page.checked("#settings-install-index"), true);
  await page.setChecked("#settings-install-index", false);
  await page.click("#settings-install-submit");
  await page.click(rowButton("monero"));
  await page.waitText("#settings-install-title", "Install Monero");
  assert.equal(await page.visible("#settings-install-index-label"), false);
  await page.click("#settings-install-submit");
  await save(page);
  await waitSaved(page);
  const requests = helper.requests.map(withoutIdentity);
  const byNode = Object.fromEntries(requests.map((request) => [request.node, request]));
  assert.equal(byNode.dogecoin.utxo_index, true);
  assert.equal(byNode.bitcoincash.utxo_index, false);
  assert.equal("utxo_index" in byNode.monero, false);
});

flow("The install dialog hides the option when the host helper cannot take it", async ({ page }) => {
  await openHostSettings(page);
  await page.click(rowButton("dogecoin"));
  await page.waitText("#settings-install-title", "Install Dogecoin");
  assert.equal(await page.visible("#settings-install-index-label"), false);
});

flow("Each node card carries a bar that goes from waiting to building to ready and hides when the index is off", { nodes: ["litecoin", "dogecoin", "bitcoincash"] }, async ({ page }) => {
  const drive = (chain, status) => page.evaluate(async (chain, status) => {
    const mod = await import("/static/utxo-index.js");
    const bar = document.getElementById(`index-bar-${chain}`);
    mod.applyIndexBar(bar, mod.indexBarView(status));
    return { hidden: bar.hidden, label: bar.querySelector(".index-bar-label").textContent, now: bar.querySelector(".progress-bg").getAttribute("aria-valuenow"), cls: bar.className };
  }, chain, status);
  for (const chain of ["bitcoin", "litecoin", "dogecoin", "bitcoincash"]) {
    assert.equal(await page.evaluate((id) => document.getElementById(id)?.hidden, `index-bar-${chain}`), true, chain);
  }
  let got = await drive("dogecoin", { enabled: true, state: "not_seeded", waiting_for: "node_sync" });
  assert.deepEqual([got.hidden, got.label, got.now], [false, "waiting for the node to sync", null]);
  assert.match(got.cls, /index-bar-indeterminate/);
  got = await drive("dogecoin", { enabled: true, state: "seeding", phase: "importing", seed_progress: 0.4 });
  assert.deepEqual([got.hidden, got.label, got.now], [false, "building, 40%", "40"]);
  got = await drive("dogecoin", { enabled: true, state: "ready" });
  assert.deepEqual([got.label, got.now], ["ready", "100"]);
  assert.match(got.cls, /index-bar-ok/);
  got = await drive("bitcoincash", { enabled: true, state: "catching_up", blocks_behind: 12 });
  assert.equal(got.label, "catching up 12 blocks");
  got = await drive("dogecoin", { enabled: false, state: "off" });
  assert.equal(got.hidden, true);
});

flow("Bitcoin's card shows its balance index bar from the dashboard's own status", { utxoIndex: true }, async ({ page, dash }) => {
  await reload(page, dash);
  assert.equal(await page.evaluate(() => !!document.querySelector("#live #index-bar-bitcoin") && !document.getElementById("utxo-index")), true, "the running layout has the bar and no index card");
  await page.waitFor(() => document.getElementById("index-bar-bitcoin")?.hidden === false, { timeout: 40000, description: "Bitcoin's index bar to be shown" });
  const { label, status } = await page.evaluate(async () => ({
    label: document.querySelector("#index-bar-bitcoin .index-bar-label").textContent,
    status: await (await fetch("/api/utxo-index", { headers: { "X-Requested-With": "fetch" } })).json(),
  }));
  assert.equal(status.enabled, true);
  assert.notEqual(label, "");
});
