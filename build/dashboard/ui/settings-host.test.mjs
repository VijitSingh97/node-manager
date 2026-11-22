import assert from "node:assert/strict";
import { answerConfirm, flow, openHostSettings, openSettings, reopenHostSettings, save, waitForFile, waitSaved, withoutIdentity } from "./harness.mjs";

const reopen = reopenHostSettings;
const savedAndSettled = waitSaved;

const toggles = [
  { action: "launch_at_boot", selector: "#settings-launch-at-start", field: "boot_enabled", defaultValue: false },
  { action: "clearnet_over_tor", selector: "#settings-clearnet-over-tor", field: "clearnet_over_tor", defaultValue: true },
  { action: "inbound_connections", selector: "#settings-inbound-connections", field: "inbound_connections", defaultValue: true },
];

for (const toggle of toggles) {
  for (const turnOn of [true, false]) {
    flow(`${toggle.action} turned ${turnOn ? "on" : "off"}`, { capability: { [toggle.field]: !turnOn } }, async ({ dash, page, helper }) => {
      await openHostSettings(page);
      assert.equal(await page.checked(toggle.selector), !turnOn);
      await page.setChecked(toggle.selector, turnOn);
      helper.hold = true;
      await save(page);
      assert.deepEqual(withoutIdentity(await waitForFile(dash, "node-control.request")), { action: toggle.action, enabled: turnOn });
      await page.waitFor((selector) => document.querySelector(selector).disabled, { args: [toggle.selector], description: "toggle to lock while the host applies" });
      assert.match(await page.text("#settings-status"), /^(Saving…|Applying host changes…)$/);
      helper.release();
      await savedAndSettled(page);
      assert.equal(helper.capability[toggle.field], turnOn);
      assert.equal(page.requestsTo("POST", "/api/settings/host").length, 1);
      assert.deepEqual(page.requestsTo("POST", "/api/settings/host")[0].body, { action: toggle.action, enabled: turnOn });
      assert.equal(await page.checked(toggle.selector), turnOn);
      await reopen(page);
      assert.equal(await page.checked(toggle.selector), turnOn);
      assert.equal(await page.disabled(toggle.selector), false);
    });
  }
}

for (const [kind, label] of [["boot", "Launch at boot"], ["login", "Launch at login"]]) {
  flow(`Launch toggle reads "${label}" when the host starts nodes at ${kind}`, { capability: { boot_kind: kind } }, async ({ page }) => {
    await openHostSettings(page);
    assert.equal(await page.text("#settings-launch-at-start-label"), label);
  });
}

flow("Launch toggle is hidden when the host cannot start nodes automatically", { capability: { boot_kind: "unsupported", boot_supported: false } }, async ({ page }) => {
  await openHostSettings(page);
  assert.equal(await page.visible("#settings-launch-at-start-label"), false);
});

flow("Host controls stay off without the host helper", { host: false }, async ({ page }) => {
  await openSettings(page);
  await page.waitText("#settings-host-status", "Host controls require the installed helper.");
  assert.equal(await page.visible("#settings-network"), false);
  assert.equal(await page.visible("#settings-balance-index"), false);
  assert.equal(await page.disabled("#settings-launch-at-start"), true);
  assert.equal(await page.text("#settings-config-path"), "Unavailable until the installed helper reports it.");
});

const indexes = [
  { name: "Bitcoin", selector: "#settings-utxo-index", field: "utxo_index", nodes: [], request: { action: "utxo_index" }, prompt: /Turn off instant Bitcoin balances\?/ },
  { name: "Litecoin", selector: "#settings-utxo-index-litecoin", field: "utxo_index_litecoin", nodes: ["litecoin"], request: { action: "utxo_index", node: "litecoin" }, prompt: /Turn off instant Litecoin balances\?/ },
];

for (const index of indexes) {
  flow(`${index.name} balance index turned on needs no confirmation`, { nodes: index.nodes }, async ({ dash, page, helper }) => {
    await openHostSettings(page);
    assert.equal(await page.checked(index.selector), false);
    await page.setChecked(index.selector, true);
    await save(page);
    await savedAndSettled(page);
    assert.equal(await page.visible("#confirm-dialog"), false);
    assert.deepEqual(helper.requests.map(withoutIdentity), [{ ...index.request, enabled: true }]);
    assert.equal(helper.capability[index.field], true);
    await reopen(page);
    assert.equal(await page.checked(index.selector), true);
  });

  flow(`${index.name} balance index turned off asks first and sends the change once confirmed`, { nodes: index.nodes, capability: { [index.field]: true } }, async ({ page, helper }) => {
    await openHostSettings(page);
    assert.equal(await page.checked(index.selector), true);
    await page.setChecked(index.selector, false);
    await save(page);
    await page.waitVisible("#confirm-dialog");
    assert.match(await page.text("#confirm-dialog-message"), index.prompt);
    await answerConfirm(page, true);
    await savedAndSettled(page);
    assert.deepEqual(helper.requests.map(withoutIdentity), [{ ...index.request, enabled: false }]);
    assert.equal(helper.capability[index.field], false);
    await reopen(page);
    assert.equal(await page.checked(index.selector), false);
  });

  flow(`${index.name} balance index stays on when the turn-off confirmation is cancelled`, { nodes: index.nodes, capability: { [index.field]: true } }, async ({ page, helper }) => {
    await openHostSettings(page);
    await page.setChecked(index.selector, false);
    await save(page);
    await answerConfirm(page, false);
    await page.waitFor((selector) => document.querySelector(selector).checked, { args: [index.selector], description: `${index.name} switch to snap back on` });
    assert.equal(helper.requests.length, 0);
    assert.equal(page.requestsTo("POST", "/api/settings/host").length, 0);
    assert.equal(helper.capability[index.field], true);
    await reopen(page);
    assert.equal(await page.checked(index.selector), true);
  });
}

flow("Litecoin balance index is hidden until Litecoin is installed", async ({ page }) => {
  await openHostSettings(page);
  assert.equal(await page.visible("#settings-utxo-index"), true);
  assert.equal(await page.visible("#settings-utxo-index-litecoin"), false);
});

const defaults = { launch_at_boot: false, clearnet_over_tor: true, inbound_connections: true, utxo_index: false, utxo_index_litecoin: false };
const flipped = { boot_enabled: true, clearnet_over_tor: false, inbound_connections: false, utxo_index: true, utxo_index_litecoin: true };

flow("Reset to default then Save sends every host switch back to its default in order", { nodes: ["litecoin"], capability: flipped }, async ({ dash, page, helper }) => {
  await openHostSettings(page);
  await page.click("#settings-reset");
  for (const [selector, wanted] of [["#settings-launch-at-start", false], ["#settings-clearnet-over-tor", true], ["#settings-inbound-connections", true], ["#settings-utxo-index", false], ["#settings-utxo-index-litecoin", false]]) {
    assert.equal(await page.checked(selector), wanted, selector);
  }
  assert.equal(helper.requests.length, 0);
  await save(page);
  await answerConfirm(page, true);
  await answerConfirm(page, true);
  await savedAndSettled(page);
  assert.deepEqual(helper.requests.map(withoutIdentity), [
    { action: "launch_at_boot", enabled: false }, { action: "clearnet_over_tor", enabled: true }, { action: "inbound_connections", enabled: true },
    { action: "utxo_index", enabled: false }, { action: "utxo_index", node: "litecoin", enabled: false },
  ]);
  assert.deepEqual({ boot: helper.capability.boot_enabled, clearnet: helper.capability.clearnet_over_tor, inbound: helper.capability.inbound_connections, utxo: helper.capability.utxo_index, ltc: helper.capability.utxo_index_litecoin }, { boot: false, clearnet: true, inbound: true, utxo: false, ltc: false });
  assert.deepEqual(dash.settings(), undefined);
});

flow("Cancel after Reset to default leaves the host switches alone", { nodes: ["litecoin"], capability: flipped }, async ({ page, helper }) => {
  await openHostSettings(page);
  await page.click("#settings-reset");
  await reopen(page);
  assert.equal(await page.checked("#settings-launch-at-start"), true);
  assert.equal(await page.checked("#settings-clearnet-over-tor"), false);
  assert.equal(await page.checked("#settings-utxo-index"), true);
  assert.equal(helper.requests.length, 0);
});

flow("Turning every host switch the other way sends one request each, in a fixed order", { nodes: ["litecoin"] }, async ({ page, helper }) => {
  await openHostSettings(page);
  for (const [selector, wanted] of [["#settings-utxo-index-litecoin", true], ["#settings-utxo-index", true], ["#settings-inbound-connections", false], ["#settings-clearnet-over-tor", false], ["#settings-launch-at-start", true]]) await page.setChecked(selector, wanted);
  await save(page);
  await savedAndSettled(page);
  assert.deepEqual(helper.requests.map(withoutIdentity), [
    { action: "launch_at_boot", enabled: true }, { action: "clearnet_over_tor", enabled: false }, { action: "inbound_connections", enabled: false },
    { action: "utxo_index", enabled: true }, { action: "utxo_index", node: "litecoin", enabled: true },
  ]);
});

flow("A failed host change shows the host's reason and does not stop the independent changes after it", async ({ page, helper }) => {
  helper.outcome = (request) => (request.action === "launch_at_boot" ? "rejected" : "applied");
  helper.detail = (request) => (request.action === "launch_at_boot" ? "Launch setting requires the host service." : "");
  await openHostSettings(page);
  await page.setChecked("#settings-launch-at-start", true);
  await page.setChecked("#settings-inbound-connections", false);
  await save(page);
  await page.waitText("#settings-status", "Launch at start: The requested host change did not pass validation. Launch setting requires the host service. Skipped: none.");
  assert.deepEqual(helper.requests.map(withoutIdentity), [{ action: "launch_at_boot", enabled: true }, { action: "inbound_connections", enabled: false }]);
  assert.equal(helper.capability.inbound_connections, false);
  assert.equal(await page.checked("#settings-launch-at-start"), false);
});

flow("A host change already in effect still lets a staged Delete go out after it", { nodes: ["litecoin"], capability: { delete_supported: true } }, async ({ dash, page, helper }) => {
  dash.writeState("node-runtime.json", { litecoin: false });
  await openHostSettings(page);
  await page.setChecked("#settings-launch-at-start", true);
  await page.click("#settings-host-nodes .settings-host-node:nth-child(4) .settings-delete-button");
  await page.type("#settings-delete-confirm", "litecoin");
  await page.click("#settings-delete-submit");
  await save(page);
  await savedAndSettled(page);
  assert.deepEqual(helper.requests.map(withoutIdentity), [{ action: "launch_at_boot", enabled: true }, { action: "delete", node: "litecoin", confirmation: "litecoin" }]);
});
