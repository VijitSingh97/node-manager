import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import test from "node:test";
import { copyText, curlCommand, shellQuote } from "./actuator-curl.js";

const shellEcho = (quoted) => execFileSync("sh", ["-c", `printf %s ${quoted}`]).toString();

test("shellQuote round-trips through sh for awkward passwords", () => {
  for (const value of ["plain", "it's", "'''", "a b  c", "$HOME `id` $(id)", "back\\slash\\", "semi;amp&pipe|", "!history", "\"dq\"", "ünï©ode"]) {
    assert.equal(shellEcho(shellQuote(value)), value, value);
  }
  assert.equal(shellQuote("it's"), "'it'\\''s'");
});

test("shellQuote rejects control characters including newlines", () => {
  for (const value of ["a\nb", "a\rb", "a\u0000b", "a\tb", "a\u007fb"]) {
    assert.throws(() => shellQuote(value), /Control characters/, JSON.stringify(value));
  }
});

test("curlCommand builds the documented command for health and info", () => {
  assert.equal(curlCommand("health", "http://mini-1.local", "admin", "pw"), "curl --user 'admin:pw' 'http://mini-1.local/actuator/health'");
  assert.equal(curlCommand("info", "https://node.example.com:8443", "admin", "it's $x"), "curl --user 'admin:it'\\''s $x' 'https://node.example.com:8443/actuator/info'");
  assert.throws(() => curlCommand("metrics", "http://x", "admin", "pw"));
  assert.throws(() => curlCommand("health", "http://x", "admin", "bad\npw"));
  const args = execFileSync("sh", ["-c", `set -- ${curlCommand("health", "http://h", "admin", "a'b").replace(/^curl /, "")}; printf '%s|' "$@"`]).toString();
  assert.equal(args, "--user|admin:a'b|http://h/actuator/health|");
});

test("copyText uses the clipboard API when it is available", async () => {
  const written = [];
  let execCalled = false;
  const ok = await copyText("cmd", { clipboard: { writeText: async (text) => written.push(text) }, exec: () => (execCalled = true) });
  assert.equal(ok, true);
  assert.deepEqual(written, ["cmd"]);
  assert.equal(execCalled, false);
});

test("copyText falls back to execCommand without a clipboard or when it rejects", async () => {
  const seen = [];
  assert.equal(await copyText("cmd", { clipboard: undefined, exec: (text) => (seen.push(text), true) }), true);
  const rejecting = { writeText: async () => { throw new Error("denied"); } };
  assert.equal(await copyText("cmd2", { clipboard: rejecting, exec: (text) => (seen.push(text), true) }), true);
  assert.deepEqual(seen, ["cmd", "cmd2"]);
});

test("copyText reports failure when every method fails", async () => {
  assert.equal(await copyText("cmd", { clipboard: undefined, exec: () => false }), false);
  assert.equal(await copyText("cmd", { clipboard: undefined, exec: () => { throw new Error("blocked"); } }), false);
  assert.equal(await copyText("cmd", { clipboard: { writeText: async () => { throw new Error("denied"); } } }), false);
});

test("copyText starts the copy synchronously so a click gesture is still active", () => {
  let sync = false;
  copyText("cmd", { clipboard: undefined, exec: () => (sync = true) });
  assert.equal(sync, true);
  let wrote = false;
  copyText("cmd", { clipboard: { writeText: async () => { wrote = true; } } });
  assert.equal(wrote, true);
});
