import assert from "node:assert/strict";
import { flow } from "./harness.mjs";

const state = {
  "monitor_state.json": {
    bitcoin: { last_block: 900000, has_last_block: true, sync_percent: 100 },
    nodes: {
      tari: { last_block: 150000, has_last_block: true, sync_percent: 100 },
      litecoin: { last_block: 2650000, has_last_block: true, sync_percent: 100 },
    },
  },
};

const card = (page, node) => page.evaluate((id) => {
  const choice = document.querySelector(`[data-select-node="${id}"]`);
  const probe = document.body.appendChild(document.createElement("i"));
  probe.style.color = "var(--unreachable)";
  const red = getComputedStyle(probe).color;
  probe.remove();
  const label = document.getElementById(`tower-overview-label-${id}`);
  return {
    unreachable: choice.dataset.nodeUnreachable,
    empty: choice.dataset.nodeEmpty,
    red: getComputedStyle(choice.querySelector(".node-choice-label b")).color === red,
    nameShown: getComputedStyle(choice.querySelector("span")).display !== "none",
    label: label.hidden ? "" : label.textContent.trim(),
  };
}, node);

const waitTower = (page, node, want) => page.waitFor((id, colour) => {
  const canvas = document.getElementById("tower");
  const rect = document.querySelector(`[data-select-node="${id}"]`).getBoundingClientRect();
  const scale = canvas.width / innerWidth;
  const { data } = canvas.getContext("2d").getImageData(Math.round((rect.left + rect.width / 2) * scale) - 2, Math.round(rect.top * scale), 5, Math.round(120 * scale));
  let painted = 0, coloured = 0;
  for (let index = 0; index < data.length; index += 4) {
    if (data[index + 3] < 128) continue;
    painted++;
    if (Math.max(data[index], data[index + 1], data[index + 2]) - Math.min(data[index], data[index + 1], data[index + 2]) > 40) coloured++;
  }
  return painted > 20 && (colour ? coloured > 20 : coloured === 0);
}, { args: [node, want === "coloured"], description: `${node} tower to be ${want}` });

const unreachable = (page, nodes, value) => page.waitFor((ids, want) =>
  ids.every((id) => document.querySelector(`[data-select-node="${id}"]`)?.dataset.nodeUnreachable === want),
{ args: [nodes, value], description: `${nodes.join(", ")} unreachable=${value}`, timeout: 45000 });

flow("Unreachable nodes keep their last tower in grey, drop the block label and show the name in red until they answer", {
  nodes: ["tari", "litecoin", "dogecoin"], rpcDown: true, settings: { tower_labels: true }, state,
}, async ({ dash, page }) => {
  await unreachable(page, ["bitcoin", "tari", "litecoin", "dogecoin"], "1");
  for (const node of ["bitcoin", "tari", "litecoin"]) {
    await waitTower(page, node, "grey");
    assert.deepEqual(await card(page, node), { unreachable: "1", empty: "0", red: true, nameShown: true, label: "" }, node);
  }
  assert.deepEqual(await card(page, "dogecoin"), { unreachable: "1", empty: "1", red: true, nameShown: true, label: "" }, "a node that never reported a height has no tower");

  dash.setRPCDown(false);
  await unreachable(page, ["bitcoin", "litecoin", "dogecoin"], "0");
  for (const node of ["bitcoin", "litecoin", "dogecoin"]) {
    await page.waitText(`#tower-overview-label-${node}`, "loading block 124");
    await waitTower(page, node, "coloured");
    assert.deepEqual(await card(page, node), { unreachable: "0", empty: "0", red: false, nameShown: true, label: "loading block 124" }, node);
  }
  assert.deepEqual(await card(page, "tari"), { unreachable: "1", empty: "0", red: true, nameShown: true, label: "" }, "Tari still does not answer");
});
