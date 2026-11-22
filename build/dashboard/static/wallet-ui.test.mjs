import { test } from "node:test";
import assert from "node:assert/strict";

function node(tag) {
  return {
    tag, className: "", textContent: "", children: [], listeners: {},
    append(...items) { this.children.push(...items); },
    replaceChildren(...items) { this.children = items; },
    addEventListener(type, fn) { this.listeners[type] = fn; },
    setAttribute() {},
  };
}
const { managerRow, replaceIfChanged } = await import("./wallet-ui.js");
globalThis.document = { createElement: node };

const KEY = "bc1q" + "a".repeat(40);
const keyOf = (row) => row.children[0].children.find((child) => child.className === "wk");
const rowFor = () => managerRow({ label: "Savings", keyText: KEY, canRemove: true, onRemove() {} });

test("an expanded address stays expanded when the list is rebuilt with changed data", () => {
  const list = node("div");
  replaceIfChanged(list, "a", () => [rowFor()]);
  keyOf(list.children[0]).listeners.click();
  assert.equal(keyOf(list.children[0]).textContent, KEY);
  assert.equal(replaceIfChanged(list, "b", () => [rowFor()]), true);
  assert.equal(keyOf(list.children[0]).textContent, KEY);
  keyOf(list.children[0]).listeners.click();
  replaceIfChanged(list, "c", () => [rowFor()]);
  assert.notEqual(keyOf(list.children[0]).textContent, KEY);
});

test("a refresh with unchanged data keeps the existing rows", () => {
  const list = node("div");
  replaceIfChanged(list, "same", () => [rowFor()]);
  const before = list.children[0];
  assert.equal(replaceIfChanged(list, "same", () => [rowFor()]), false);
  assert.equal(list.children[0], before);
});
