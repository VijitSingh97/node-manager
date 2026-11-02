import assert from "node:assert/strict";
import test from "node:test";
import { dashboardFetch, signOut } from "./session.js";

test("dashboardFetch preserves requests and redirects same-origin API 401 responses", async (t) => {
  const originalFetch = globalThis.fetch;
  const originalWindow = globalThis.window;
  const originalDocument = globalThis.document;
  t.after(() => {
    globalThis.fetch = originalFetch;
    if (originalWindow === undefined) delete globalThis.window; else globalThis.window = originalWindow;
    if (originalDocument === undefined) delete globalThis.document; else globalThis.document = originalDocument;
  });
  const redirects = [];
  globalThis.window = { location: { href: "https://node.local/", origin: "https://node.local", replace: (path) => redirects.push(path) } };
  globalThis.document = { baseURI: "https://node.local/" };
  const init = { method: "POST", body: "payload" };
  let received;
  const response = { status: 401, ok: false };
  globalThis.fetch = async (input, options) => {
    received = { input, options };
    return response;
  };
  assert.equal(await dashboardFetch("/api/settings", init), response);
  assert.deepEqual(received, { input: "/api/settings", options: init });
  assert.deepEqual(redirects, ["/login"]);

  await dashboardFetch("https://prices.example/api/value", init);
  assert.deepEqual(redirects, ["/login"]);
});

test("dashboardFetch propagates fetch failures without browser globals", async (t) => {
  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });
  globalThis.fetch = async () => ({ status: 401 });
  assert.equal((await dashboardFetch("/api/status")).status, 401);
  const failure = new Error("offline");
  globalThis.fetch = async () => { throw failure; };
  await assert.rejects(dashboardFetch("/api/status"), failure);
});

test("signOut posts without storing credentials and navigates after success", async () => {
  let request;
  const redirects = [];
  const response = { ok: true };
  const result = await signOut(async (input, init) => {
    request = { input, init };
    return response;
  }, { replace: (path) => redirects.push(path) });
  assert.equal(result, response);
  assert.deepEqual(request, { input: "/logout", init: { method: "POST", headers: { "X-Requested-With": "fetch" } } });
  assert.deepEqual(redirects, ["/login"]);

  await signOut(async () => ({ ok: false }), { replace: (path) => redirects.push(path) });
  assert.deepEqual(redirects, ["/login"]);
});
