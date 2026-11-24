import { NODE_LABELS } from "./chains.js";
import { CSRF_HEADERS, dashboardFetch as fetch } from "./session.js";

export const CATEGORY_LABELS = {
  node: "Nodes",
  chain: "Sync and blocks",
  host: "Host",
  settings: "Settings",
  security: "Sign-in and credentials",
  index: "Balance index",
  wallet: "Wallets",
  system: "Updates and restarts",
  alerts: "Alert delivery",
};
const SEVERITY_LABELS = { warning: "Warning", error: "Error" };
const localTime = (date) => date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });

export function eventsPath({ category = "", node = "", before = 0 } = {}) {
  const query = new URLSearchParams();
  if (category) query.set("category", category);
  if (node) query.set("node", node);
  if (before) query.set("before", String(before));
  const text = query.toString();
  return text ? `/api/events?${text}` : "/api/events";
}

export function eventView(event, format = localTime) {
  const datetime = String(event?.time || "");
  const date = new Date(datetime);
  const severity = SEVERITY_LABELS[event?.severity] || "";
  return {
    datetime,
    time: Number.isNaN(date.getTime()) ? datetime : format(date),
    category: CATEGORY_LABELS[event?.category] || "Other",
    severity,
    tone: severity ? event.severity : "info",
    text: String(event?.text || ""),
  };
}

export function emptyMessage(filtered) {
  return filtered ? "No events match these filters." : "No events yet.";
}

function eventItem(view) {
  const item = document.createElement("li");
  item.className = `event event-${view.tone}`;
  const meta = document.createElement("div");
  meta.className = "event-meta";
  const time = document.createElement("time");
  time.dateTime = view.datetime;
  time.textContent = view.time;
  const category = document.createElement("span");
  category.textContent = view.category;
  meta.append(time, category);
  if (view.severity) {
    const severity = document.createElement("span");
    severity.className = "event-severity";
    severity.textContent = view.severity;
    meta.append(severity);
  }
  const text = document.createElement("p");
  text.className = "event-text";
  text.textContent = view.text;
  item.append(meta, text);
  return item;
}

export function eventsController(root = document) {
  const byId = (id) => root.getElementById(`events-${id}`);
  const [dialog, list, status, more, category, node] = ["dialog", "list", "status", "more", "category", "node"].map(byId);
  const option = ([value, label]) => Object.assign(document.createElement("option"), { value, textContent: label });
  category.append(...Object.entries(CATEGORY_LABELS).map(option));
  node.append(...Object.entries(NODE_LABELS).map(option));
  let next = 0;
  let request = 0;
  const load = async (older) => {
    const ticket = ++request;
    const refocus = older && root.activeElement === more;
    more.disabled = true;
    if (!older) {
      list.replaceChildren();
      next = 0;
      more.hidden = true;
    }
    status.textContent = older ? "Loading older events…" : "Loading events…";
    try {
      const response = await fetch(eventsPath({ category: category.value, node: node.value, before: older ? next : 0 }), { headers: CSRF_HEADERS });
      if (!response.ok) throw new Error((await response.text()).trim().slice(0, 200) || `HTTP ${response.status}`);
      const data = await response.json();
      if (ticket !== request) return;
      list.append(...(data.events || []).map((event) => eventItem(eventView(event))));
      next = data.next || 0;
      status.textContent = list.children.length ? "" : emptyMessage(!!(category.value || node.value));
    } catch (error) {
      if (ticket !== request) return;
      status.textContent = `Events could not be loaded: ${error.message}`;
    }
    more.disabled = false;
    more.hidden = !next;
    if (refocus) (more.hidden ? list : more).focus();
  };
  byId("open").addEventListener("click", () => {
    dialog.showModal();
    load(false);
  });
  byId("close").addEventListener("click", () => dialog.close());
  category.addEventListener("change", () => load(false));
  node.addEventListener("change", () => load(false));
  more.addEventListener("click", () => load(true));
  return { load };
}

if (typeof document !== "undefined" && document.getElementById("events-dialog")) eventsController();
