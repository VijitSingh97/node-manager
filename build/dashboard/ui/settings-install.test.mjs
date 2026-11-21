import assert from "node:assert/strict";
import { flow, nodeIDs, openHostSettings, reopenHostSettings, save, waitForFile, waitSaved, withoutIdentity } from "./harness.mjs";

const names = { bitcoin: "Bitcoin", monero: "Monero", tari: "Tari", litecoin: "Litecoin", dogecoin: "Dogecoin", bitcoincash: "Bitcoin Cash", zcash: "Zcash" };
const row = (node) => `#settings-host-nodes .settings-host-node:nth-child(${nodeIDs.indexOf(node) + 1})`;
const rowButton = (node) => `${row(node)} button`;
const reopen = reopenHostSettings;
const savedAndSettled = waitSaved;

flow("Cancel throws away staged host switches and installs", async ({ page, helper }) => {
  await openHostSettings(page);
  await page.setChecked("#settings-launch-at-start", true);
  await page.click(rowButton("monero"));
  await page.click("#settings-install-submit");
  await page.waitText(rowButton("monero"), "Installs on Save");
  await reopen(page);
  assert.equal(await page.checked("#settings-launch-at-start"), false);
  assert.equal(await page.text(rowButton("monero")), "Install");
  assert.equal(helper.requests.length, 0);
});

const revision = "ab12".repeat(16);

flow("Run new config shows only while a change is pending and runs on Save", { capability: { config_pending: true, config_revision: revision } }, async ({ page, helper }) => {
  await openHostSettings(page);
  await page.waitVisible("#settings-config-change");
  await page.waitText("#settings-apply-config", "Run new config");
  for (const selector of ["#settings-launch-at-start", "#settings-clearnet-over-tor", "#settings-inbound-connections", "#settings-utxo-index", rowButton("monero")]) {
    await page.waitFor((sel) => document.querySelector(sel).disabled, { args: [selector], description: `${selector} to lock while a config is pending` });
  }
  await page.waitFor(() => !document.getElementById("settings-apply-config").disabled, { description: "Run new config to enable" });

  await page.click("#settings-apply-config");
  await page.waitText("#settings-apply-config", "Runs on Save");
  await page.click("#settings-apply-config");
  await page.waitText("#settings-apply-config", "Run new config");

  await page.click("#settings-apply-config");
  await page.waitText("#settings-apply-config", "Runs on Save");
  await reopen(page);
  await page.waitText("#settings-apply-config", "Run new config");
  assert.equal(helper.requests.length, 0);

  await page.click("#settings-apply-config");
  await save(page);
  await savedAndSettled(page);
  assert.deepEqual(helper.requests.map(withoutIdentity), [{ action: "apply_config", config_revision: revision }]);
  await page.waitFor(() => document.getElementById("settings-config-change").hidden);
  assert.equal(await page.disabled("#settings-launch-at-start"), false);
});

flow("Run new config hides after a helper that takes longer than a poll to apply it", { capability: { config_pending: true, config_revision: revision } }, async ({ page, helper }) => {
  // Blocks the helper between taking the request and reporting it, like the real helper restarting containers.
  helper.outcome = () => (Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 3000), "applied");
  await openHostSettings(page);
  await page.click("#settings-apply-config");
  await save(page);
  await savedAndSettled(page);
  await page.waitFor(() => document.getElementById("settings-config-change").hidden, { description: "Run new config to hide" });
});

flow("Run new config is hidden when nothing is pending", async ({ page }) => {
  await openHostSettings(page);
  assert.equal(await page.visible("#settings-config-change"), false);
});

const installable = nodeIDs.filter((node) => node !== "bitcoin");

flow("Install dialog rejects a blank or relative data folder and Cancel stages nothing", async ({ page, helper }) => {
  await openHostSettings(page);
  await page.click(rowButton("monero"));
  await page.waitVisible("#settings-install-dialog");
  assert.equal(await page.text("#settings-install-title"), "Install Monero");
  assert.equal(await page.value("#settings-install-data-dir"), "/srv/node/monero");
  assert.equal(await page.text("#settings-install-submit"), "Install on Save");
  assert.equal(await page.evaluate(() => document.querySelectorAll("#settings-install-interfaces [data-interface-enabled]").length), 2);

  await page.type("#settings-install-data-dir", "");
  await page.click("#settings-install-submit");
  assert.equal(await page.visible("#settings-install-dialog"), true);
  assert.equal(await page.text(rowButton("monero")), "Install");

  await page.type("#settings-install-data-dir", "relative/folder");
  await page.click("#settings-install-submit");
  await page.waitText("#settings-install-error", "Choose an absolute host folder and valid ports from 1 to 65535.");

  await page.click("#settings-install-cancel");
  await page.waitFor(() => !document.getElementById("settings-install-dialog").open);
  assert.equal(await page.text(rowButton("monero")), "Install");
  await save(page, "No changes.");
  assert.equal(helper.requests.length, 0);
});

flow("Install stages with every interface closed, can be undone, and installs on Save", async ({ page, helper }) => {
  await openHostSettings(page);
  await page.click(rowButton("monero"));
  await page.click("#settings-install-submit");
  await page.waitText(rowButton("monero"), "Installs on Save");
  assert.equal(await page.evaluate((selector) => document.querySelector(selector).title, rowButton("monero")), "Click to undo");
  await page.click(rowButton("monero"));
  assert.equal(await page.text(rowButton("monero")), "Install");
  await save(page, "No changes.");

  await page.click(rowButton("monero"));
  await page.click("#settings-install-submit");
  await save(page);
  await savedAndSettled(page);
  assert.deepEqual(helper.requests.map(withoutIdentity), [{
    action: "install", node: "monero", data_dir: "/srv/node/monero",
    interfaces: [{ id: "monero_rpc", enabled: false, host_port: 18081 }, { id: "monero_zmq", enabled: false, host_port: 18083 }],
  }]);
});

flow("Install sends each node's chosen folder and open interfaces", async ({ page, helper }) => {
  await openHostSettings(page);
  for (const node of installable) {
    await page.click(rowButton(node));
    await page.waitText("#settings-install-title", `Install ${names[node]}`);
    assert.equal(await page.value("#settings-install-data-dir"), `/srv/node/${node}`);
    const interfaces = await page.evaluate(() => Array.from(document.querySelectorAll("#settings-install-interfaces [data-interface]"), (item) => ({ id: item.dataset.interface, port: Number(item.querySelector("[data-interface-port]").value), portDisabled: item.querySelector("[data-interface-port]").disabled })));
    assert.ok(interfaces.length >= 1);
    assert.ok(interfaces.every((item) => item.portDisabled), "ports stay locked until the interface is opened");
    await page.type("#settings-install-data-dir", `/srv/custom/${node}`);
    await page.click(`#settings-install-interfaces [data-interface="${interfaces[0].id}"] [data-interface-enabled]`);
    await page.type(`#settings-install-interfaces [data-interface="${interfaces[0].id}"] [data-interface-port]`, "29999");
    await page.click("#settings-install-submit");
    await page.waitText(rowButton(node), "Installs on Save");
    await save(page);
    await savedAndSettled(page);
    const request = withoutIdentity(helper.requests.at(-1));
    assert.deepEqual(request, {
      action: "install", node, data_dir: `/srv/custom/${node}`,
      interfaces: interfaces.map((item, position) => ({ id: item.id, enabled: position === 0, host_port: position === 0 ? 29999 : item.port })),
    });
    await page.waitText(rowButton(node), "Install");
  }
  assert.equal(helper.requests.length, installable.length);
});

flow("Move data guards the current folder, stages, undoes and moves on Save for every node", { nodes: installable }, async ({ page, helper }) => {
  await openHostSettings(page);
  for (const node of nodeIDs) {
    assert.equal(await page.text(`${row(node)} .settings-host-path`), `/srv/node/${node}`);
    assert.equal(await page.text(rowButton(node)), "Move data…");
    await page.click(rowButton(node));
    await page.waitText("#settings-install-title", `Move ${names[node]} data`);
    assert.equal(await page.value("#settings-install-data-dir"), `/srv/node/${node}`);
    assert.equal(await page.text("#settings-install-submit"), "Move on Save");

    await page.click("#settings-install-submit");
    await page.waitText("#settings-install-error", "Choose a different folder than the current one.");
    assert.equal(await page.visible("#settings-install-dialog"), true);
    await page.click("#settings-install-cancel");
    await page.waitFor(() => !document.getElementById("settings-install-dialog").open);
    assert.equal(await page.text(rowButton(node)), "Move data…");

    await page.click(rowButton(node));
    await page.type("#settings-install-data-dir", `/srv/moved/${node}`);
    await page.click("#settings-install-submit");
    await page.waitText(rowButton(node), "Moves on Save");
    await page.click(rowButton(node));
    assert.equal(await page.text(rowButton(node)), "Move data…");

    await page.click(rowButton(node));
    await page.type("#settings-install-data-dir", `/srv/moved/${node}`);
    await page.click("#settings-install-submit");
    await save(page);
    await savedAndSettled(page);
    assert.deepEqual(withoutIdentity(helper.requests.at(-1)), { action: "migrate", node, data_dir: `/srv/moved/${node}` });
  }
  assert.equal(helper.requests.length, nodeIDs.length);
});

flow("Install and Move data stay locked while the host is busy", { nodes: ["litecoin"] }, async ({ dash, page, helper }) => {
  await openHostSettings(page);
  helper.hold = true;
  await page.setChecked("#settings-launch-at-start", true);
  await save(page);
  await waitForFile(dash, "node-control.request");
  await page.waitFor(() => document.getElementById("settings-launch-at-start").disabled);
  assert.equal(await page.disabled(rowButton("monero")), true);
  assert.equal(await page.disabled(rowButton("litecoin")), true);
  helper.release();
  await savedAndSettled(page);
  await page.waitFor((selector) => !document.querySelector(selector).disabled, { args: [rowButton("monero")] });
});
