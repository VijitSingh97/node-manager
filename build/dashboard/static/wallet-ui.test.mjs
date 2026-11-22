import { test } from "node:test";
import assert from "node:assert/strict";

function node(tag) {
  return {
    tag, className: "", textContent: "", children: [], listeners: {},
    append(...items) { items.forEach((item) => { item.parentNode = this; }); this.children.push(...items); },
    replaceChildren(...items) { items.forEach((item) => { item.parentNode = this; }); this.children = items; },
    contains(other) { return other === this || this.children.some((child) => child.contains(other)); },
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

test("a focused row control keeps focus when the list is rebuilt with changed data", () => {
  const list = node("div");
  const rowWithButton = () => {
    const row = rowFor();
    const button = node("button");
    button.focus = function focus() { document.activeElement = this; };
    row.append(button);
    return row;
  };
  replaceIfChanged(list, "a", () => [rowWithButton()]);
  const old = list.children[0].children.at(-1);
  document.activeElement = old;
  replaceIfChanged(list, "b", () => [rowWithButton()]);
  const fresh = list.children[0].children.at(-1);
  assert.notEqual(fresh, old);
  assert.equal(document.activeElement, fresh);
});
