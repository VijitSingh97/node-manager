import assert from "node:assert/strict";
import test from "node:test";
import { CATEGORY_LABELS, emptyMessage, eventView, eventsPath } from "./events.js";

test("eventsPath sends only the filters and cursor that are set", () => {
  assert.equal(eventsPath(), "/api/events");
  assert.equal(eventsPath({ category: "node" }), "/api/events?category=node");
  assert.equal(eventsPath({ category: "security", node: "bitcoincash", before: 4096 }), "/api/events?category=security&node=bitcoincash&before=4096");
  assert.equal(eventsPath({ node: "", before: 0 }), "/api/events");
});

test("eventView labels category and severity in words, not only colour", () => {
  const format = (date) => date.toISOString();
  assert.deepEqual(eventView({ time: "2026-11-20T10:00:00Z", category: "node", severity: "error", text: "Bitcoin: 🔴 node down" }, format), {
    datetime: "2026-11-20T10:00:00Z", time: "2026-11-20T10:00:00.000Z", category: "Nodes", severity: "Error", tone: "error", text: "Bitcoin: 🔴 node down",
  });
  assert.deepEqual(eventView({ time: "2026-11-20T10:00:00Z", category: "host", severity: "warning", text: "thermal throttle" }, format).tone, "warning");
  const info = eventView({ time: "2026-11-20T10:00:00Z", category: "settings", severity: "info", text: "Settings changed: low_disk_gib." }, format);
  assert.equal(info.severity, "");
  assert.equal(info.tone, "info");
});

test("eventView tolerates unknown or missing fields", () => {
  assert.deepEqual(eventView({ time: "yesterday", category: "future", severity: "fatal", text: 42 }), {
    datetime: "yesterday", time: "yesterday", category: "Other", severity: "", tone: "info", text: "42",
  });
  assert.equal(eventView(undefined).text, "");
});

test("every API category has a label and the empty state names the filters", () => {
  assert.deepEqual(Object.keys(CATEGORY_LABELS), ["node", "chain", "host", "settings", "security", "index", "wallet", "system", "alerts"]);
  assert.equal(emptyMessage(false), "No events yet.");
  assert.equal(emptyMessage(true), "No events match these filters.");
});

test("the Events button sits between User and Settings and opens a labelled dialog", async () => {
  const { readFile } = await import("node:fs/promises");
  const html = await readFile(new URL("../testdata/index.rendered.html", import.meta.url), "utf8");
  const at = (marker) => html.indexOf(marker);
  assert.ok(at('id="account-open"') < at('id="events-open"') && at('id="events-open"') < at('id="settings-open"'));
  assert.match(html, /<button id="events-open" class="settings-open events-open" type="button" aria-label="Events"/);
  assert.match(html, /<dialog id="events-dialog" class="settings-dialog events-dialog" aria-labelledby="events-title">/);
  assert.match(html, /<p id="events-status" class="settings-note" role="status" aria-live="polite">/);
  assert.match(html, /<script type="module" src="\/static\/events.js"><\/script>/);
});
