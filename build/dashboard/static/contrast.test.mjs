import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("./dashboard.css", import.meta.url), "utf8");

function tokens(block) {
  return Object.fromEntries([...block.matchAll(/--([\w-]+):\s*(#[0-9a-f]{3,6})\s*;/gi)].map((m) => [m[1], m[2]]));
}
function blockAfter(marker) {
  const start = css.indexOf(marker);
  assert.ok(start >= 0, marker);
  return css.slice(start, css.indexOf("}", start));
}

const themes = {
  dark: tokens(blockAfter(":root {")),
  light: tokens(blockAfter(':root[data-theme="light"] {')),
  auto: tokens(blockAfter(':root:not([data-theme="dark"]) {')),
};

function luminance(hex) {
  let h = hex.slice(1);
  if (h.length === 3) h = [...h].map((c) => c + c).join("");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16) / 255).map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
function ratio(a, b) {
  const [x, y] = [luminance(a), luminance(b)];
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

const pairs = [
  ["text", "bg"], ["text", "card"], ["muted", "bg"], ["muted", "card"],
  ["faint", "bg"], ["accent", "bg"], ["accent", "card"], ["bg", "accent"],
  ["unreachable", "bg"], ["unreachable", "card"],
];

test("light theme tokens are the same in the explicit and OS-driven blocks", () => {
  assert.deepEqual(themes.auto, themes.light);
});

for (const [theme, values] of Object.entries(themes)) {
  for (const [fg, bg] of pairs) {
    test(`${theme}: --${fg} on --${bg} reaches 4.5:1`, () => {
      assert.ok(values[fg] && values[bg], `${fg}/${bg} defined`);
      const got = ratio(values[fg], values[bg]);
      assert.ok(got >= 4.5, `${values[fg]} on ${values[bg]} is ${got.toFixed(2)}:1`);
    });
  }
}

test("delete confirmation button text reaches 4.5:1 on its fixed background", () => {
  const rule = css.match(/#settings-delete-submit \{[^}]*\}/)[0];
  const bg = rule.match(/background:\s*(#[0-9a-f]{3,6})/i)[1];
  const fg = rule.match(/\bcolor:\s*(#[0-9a-f]{3,6})/i)[1];
  assert.ok(ratio(fg, bg) >= 4.5);
});

test("warning, notice, error and delete text use theme tokens, not fixed colours", () => {
  for (const selector of [".watch-warn", ".update-notice", ".watch-msg.watch-msg-error", ".settings-delete-warning", ".warn", ".family-watch-error"]) {
    const rule = css.match(new RegExp(`${selector.replace(/\./g, "\\.")} \\{[^}]*\\}`))[0];
    assert.match(rule, /color:\s*var\(--/, selector);
  }
});

test("accent-filled add button takes its text colour from a theme token", () => {
  const rule = css.match(/\.watch-add button \{[^}]*\}/)[0];
  assert.match(rule, /background:\s*var\(--accent\)/);
  assert.match(rule, /\bcolor:\s*var\(--bg\)/);
});

const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
const nodes = ["bitcoin", "monero", "tari", "litecoin", "dogecoin", "bitcoincash", "zcash"];

for (const [theme, values] of Object.entries(themes)) {
  for (const node of nodes) {
    for (const bg of ["bg", "card", "tag-bg"]) {
      test(`${theme}: ${node} text reaches 4.5:1 on --${bg}`, () => {
        const got = ratio(values[`nt-${node}`], values[bg]);
        assert.ok(got >= 4.5, `${values[`nt-${node}`]} on ${values[bg]} is ${got.toFixed(2)}:1`);
      });
    }
    test(`${theme}: ${node} focus ring reaches 3:1 on --bg and --card`, () => {
      for (const bg of ["bg", "card"]) assert.ok(ratio(values[`nt-${node}`], values[bg]) >= 3, `${node} on ${bg}`);
    });
  }
  test(`${theme}: muted text reaches 4.5:1 on --tag-bg`, () => {
    assert.ok(ratio(values.muted, values["tag-bg"]) >= 4.5);
  });
  test(`${theme}: --monero text reaches 4.5:1 on --bg and --card`, () => {
    for (const bg of ["bg", "card"]) assert.ok(ratio(values.monero, values[bg]) >= 4.5, bg);
  });
}

test("every inline node accent has its matching text token and the ring uses it", () => {
  const brand = { "#f2a900": "bitcoin", "#ff6600": "monero", "#8f5cff": "tari", "#4d8fd8": "litecoin", "#b88746": "dogecoin", "#36a852": "bitcoincash", "#d69c35": "zcash" };
  const found = [...html.matchAll(/--node-accent:(#[0-9a-f]{6})([^"]*)"/gi)];
  assert.ok(found.length > 20);
  for (const [, hex, rest] of found) assert.ok(rest.includes(`--node-text:var(--nt-${brand[hex.toLowerCase()]})`), hex);
  assert.match(css, /\.node-choice:focus-visible span \{[^}]*var\(--node-text/);
});
