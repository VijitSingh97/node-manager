import { test } from "node:test";
import assert from "node:assert/strict";

function node(tag) {
  return {
    tag, className: "", textContent: "", children: [], listeners: {},
    append(...items) { items.forEach((item) => { item.parentNode = this; }); this.children.push(...items); },
    replaceChildren(...items) { items.forEach((item) => { item.parentNode = this; }); this.children = items; },
    contains(other) { return other === this || this.children.some((child) => child.contains(other)); },
    addEventListener(type, fn) { this.listeners[type] = fn; },
    setAttribute(name, value) { (this.attrs ||= {})[name] = value; },
    getAttribute(name) { return this.attrs?.[name]; },
  };
}
const { managerRow, replaceIfChanged } = await import("./wallet-ui.js");
globalThis.document = { createElement: node, querySelector: () => (globalThis.openDialog ? {} : null) };

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

const focusable = (label) => () => {
  const row = managerRow({ label, canRemove: true, onRemove() {} });
  const button = row.children.at(-1);
  button.focus = function focus() { document.activeElement = this; };
  return row;
};

test("focus follows the same wallet when an earlier row is removed", () => {
  const list = node("div");
  replaceIfChanged(list, "a", () => ["1", "2", "3"].map((l) => focusable(l)()));
  document.activeElement = list.children[1].children.at(-1);
  replaceIfChanged(list, "b", () => ["2", "3"].map((l) => focusable(l)()));
  assert.equal(document.activeElement, list.children[0].children.at(-1));
});

test("focus does not move to another row when the focused row is removed", () => {
  const list = node("div");
  replaceIfChanged(list, "a", () => ["1", "2", "3"].map((l) => focusable(l)()));
  const old = list.children[1].children.at(-1);
  document.activeElement = old;
  replaceIfChanged(list, "b", () => ["1", "3"].map((l) => focusable(l)()));
  assert.equal(document.activeElement, old);
});

test("a rebuild is skipped while a dialog is open and happens once it closes", () => {
  const list = node("div");
  replaceIfChanged(list, "a", () => [rowFor()]);
  globalThis.openDialog = true;
  assert.equal(replaceIfChanged(list, "b", () => [rowFor()]), false);
  globalThis.openDialog = false;
  assert.equal(replaceIfChanged(list, "b", () => [rowFor()]), true);
});

test("a build that throws does not record the render key", () => {
  const list = node("div");
  assert.throws(() => replaceIfChanged(list, "a", () => { throw new Error("boom"); }));
  assert.equal(replaceIfChanged(list, "a", () => [rowFor()]), true);
});
