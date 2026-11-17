import assert from "node:assert/strict";
import { flow, waitForFile, withoutIdentity } from "./harness.mjs";

const names = { bitcoin: "Bitcoin", litecoin: "Litecoin" };

async function openNode(page, node) {
  await page.waitVisible(`[data-select-node="${node}"]`);
  await page.click(`[data-select-node="${node}"]`);
  await page.waitVisible(`[data-node-power="${node}"]`);
}

const button = (node) => `[data-node-power="${node}"]`;

for (const node of Object.keys(names)) {
  flow(`${names[node]} can be deactivated only by typing the word and activated again`, { nodes: node === "bitcoin" ? [] : [node] }, async ({ dash, page, helper }) => {
    await openNode(page, node);
    await page.waitFor((selector) => document.querySelector(selector).textContent === "Deactivate node" && !document.querySelector(selector).disabled, { args: [button(node)], description: "power button to be ready" });

    await page.click(button(node));
    await page.waitText("#node-power-title", `Deactivate ${names[node]}`);
    assert.equal(await page.visible("#node-power-confirm"), true);
    assert.equal(await page.disabled("#node-power-submit"), true);
    for (const wrong of ["Deactivate", "yes", "deactivat", "deactivate "]) {
      await page.type("#node-power-confirm", wrong);
      assert.equal(await page.disabled("#node-power-submit"), true, `"${wrong}" must not unlock the button`);
    }
    await page.click("#node-power-cancel");
    await page.waitFor(() => !document.getElementById("node-power-dialog").open);
    assert.equal(dash.readJSON("node-control.request"), undefined);
    assert.equal(helper.requests.length, 0);

    await page.click(button(node));
    assert.equal(await page.value("#node-power-confirm"), "");
    assert.equal(await page.disabled("#node-power-submit"), true);
    await page.type("#node-power-confirm", "deactivate");
    assert.equal(await page.disabled("#node-power-submit"), false);
    helper.hold = true;
    await page.press("Enter");
    assert.deepEqual(withoutIdentity(await waitForFile(dash, "node-control.request")), { node, active: false, confirmation: "deactivate" });
    await page.waitFor(() => !document.getElementById("node-power-dialog").open);
    await page.waitText(button(node), "Deactivating…");
    helper.release();
    await page.waitText(`#${node}-power-status`, `${names[node]} deactivated.`);
    await page.waitText(button(node), "Activate node");

    await page.click(button(node));
    await page.waitText("#node-power-title", `Activate ${names[node]}`);
    assert.equal(await page.visible("#node-power-confirm"), false);
    assert.equal(await page.disabled("#node-power-submit"), false);
    assert.equal(await page.text("#node-power-submit"), "Activate node");
    helper.hold = true;
    await page.click("#node-power-submit");
    assert.deepEqual(withoutIdentity(await waitForFile(dash, "node-control.request")), { node, active: true, confirmation: "" });
    await page.waitText(button(node), "Activating…");
    helper.release();
    await page.waitText(`#${node}-power-status`, `${names[node]} activated.`);
    await page.waitText(button(node), "Restarting…");
    if (node !== "bitcoin") await page.waitText(`#${node}-badge`, "Restarting");
    assert.equal(await page.disabled(button(node)), true);
    assert.deepEqual(page.requestsTo("POST", `/api/nodes/${node}/power`).map((request) => request.body), [{ active: false, confirmation: "deactivate" }, { active: true, confirmation: "" }]);
  });
}

flow("A stopped node offers Start node, which needs no confirmation word", { hostStats: { running: { bitcoin: false } } }, async ({ dash, page, helper }) => {
  await openNode(page, "bitcoin");
  await page.waitText(button("bitcoin"), "Start node");
  await page.click(button("bitcoin"));
  await page.waitText("#node-power-title", "Start Bitcoin");
  assert.equal(await page.visible("#node-power-confirm"), false);
  assert.equal(await page.disabled("#node-power-submit"), false);
  helper.hold = true;
  await page.click("#node-power-submit");
  assert.deepEqual(withoutIdentity(await waitForFile(dash, "node-control.request")), { node: "bitcoin", active: true, confirmation: "" });
  await page.waitText(button("bitcoin"), "Starting…");
  helper.release();
  await page.waitText("#bitcoin-power-status", "Bitcoin activated.");
  await page.waitText(button("bitcoin"), "Restarting…");
  assert.equal(await page.disabled(button("bitcoin")), true);
});

flow("Node power stays disabled without the host helper", { host: false }, async ({ page }) => {
  await openNode(page, "bitcoin");
  await page.waitFor((selector) => document.querySelector(selector).disabled, { args: [button("bitcoin")] });
  assert.equal(await page.text("#bitcoin-power-status"), "Node controls require the installed host helper.");
});
