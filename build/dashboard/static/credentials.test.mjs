import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { credentialControlModel, passwordChangeError, shouldPollCredentials } from "./credentials.js";

test("dashboard password changes require the current password and validate the replacement", () => {
  assert.equal(passwordChangeError("", "twelve-chars", "twelve-chars"), "Enter your current dashboard password.");
  assert.equal(passwordChangeError("", "twelve-chars", "twelve-chars", false), "");
  assert.equal(passwordChangeError("old", "twelve-chars", "twelve-chars"), "");
  assert.equal(passwordChangeError("old", "short", "short"), "The new password must be at least 12 characters.");
  assert.equal(passwordChangeError("old", "twelve-chars", "different-one"), "The new passwords do not match.");
  assert.equal(passwordChangeError("old", "twelve-\nchars", "twelve-\nchars"), "Passwords cannot contain control characters.");
  assert.equal(passwordChangeError("old", "a".repeat(129), "a".repeat(129)), "The new password must be no more than 128 characters.");
});

test("credential status exposes only supported LAN RPC interfaces", () => {
  assert.deepEqual(credentialControlModel({
    available: true,
    interfaces: [
      { node: "bitcoin", username: "admin", password: "must-not-copy", enabled: true },
      { node: "tari", enabled: true },
      { node: "monero", enabled: "true" },
    ],
    status: { state: "applying", node: "bitcoin", message: "Restarting Bitcoin RPC…" },
  }), {
    available: true,
    requiresPassword: false,
    interfaces: {
      bitcoin: { node: "bitcoin", enabled: true },
      monero: { node: "monero", enabled: false },
    },
    status: { state: "applying", node: "bitcoin", message: "Restarting Bitcoin RPC…" },
  });
  assert.deepEqual(credentialControlModel({ available: "true", state: "invented", status: "ignored" }), {
    available: false, requiresPassword: false, interfaces: {}, status: { state: "", node: "", message: "" },
  });
});

test("credential polling runs only while a reset is active and settings remain open", () => {
  assert.equal(shouldPollCredentials(true, "pending"), true);
  assert.equal(shouldPollCredentials(true, "applying"), true);
  assert.equal(shouldPollCredentials(false, "pending"), false);
  assert.equal(shouldPollCredentials(true, "applied"), false);
});

test("credential controls are independent, limited to password-capable RPCs, and never persist secrets", async () => {
  const html = await readFile(new URL("../testdata/index.rendered.html", import.meta.url), "utf8");
  const source = await readFile(new URL("./credentials.js", import.meta.url), "utf8");
  const account = html.slice(html.indexOf('<dialog id="account-dialog"'), html.indexOf('<dialog id="settings-dialog"'));
  assert.ok(html.indexOf('id="account-open"') < html.indexOf('id="settings-open"'));
  assert.ok(account.indexOf('id="settings-account"') > 0);
  assert.ok(account.indexOf('id="settings-account"') < account.indexOf('id="settings-dashboard-password"'));
  assert.doesNotMatch(html.slice(html.indexOf('<dialog id="settings-dialog"')), /settings-dashboard-password|session-logout/);
  assert.match(html, /id="settings-password-save" type="button"/);
  assert.match(html, /id="settings-password-reload" type="button"/);
  assert.match(html, /id="credential-reset-cancel"[^>]*type="button"|type="button" id="credential-reset-cancel"/);
  assert.deepEqual([...html.matchAll(/data-credential-node="([^"]+)"/g)].map((match) => match[1]), [
    "bitcoin", "monero", "litecoin", "dogecoin", "bitcoincash", "zcash",
  ]);
  assert.doesNotMatch(source, /localStorage|sessionStorage/);
  assert.match(source, /closeNode\(\) \{[\s\S]*generation \+= 1;[\s\S]*clearTimeout\(poll\);/);
  assert.match(source, /openNode\(\) \{[\s\S]*load\(generation\);/);
});
