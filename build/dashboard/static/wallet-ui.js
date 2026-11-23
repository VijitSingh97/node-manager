import { points } from "./sparkline.js";
import { towerLayout, WIDE_MIN } from "./tower.js";
import { CSRF_HEADERS, dashboardFetch as fetch } from "./session.js";
import { watchPrice, walletCurrency } from "./prices.js";
import { askConfirm } from "./confirm-dialog.js";
import { fiatLabel, httpsURL, shortKey } from "./wallet-format.js";

const STALE_TITLE = "Last known balance; waiting for a fresh result";

export function rowView(row, price, currency) {
  const balance = row.amount ? `${row.amount} ${row.unit}` : row.stateText || "—";
  return {
    name: row.note ? `${row.label}\u00a0ⓘ` : row.label,
    nameTitle: row.note || "",
    nameAria: row.note ? `${row.label}. ${row.note}` : row.label,
    balance,
    balanceAria: `${row.label}: ${balance}`,
    stale: row.stale === true,
    fiat: row.amount && row.fiatAsset ? fiatLabel(row.value, price, currency) : "",
    status: row.status || "",
  };
}

export function totalView(total, price, currency) {
  return {
    balance: total.text,
    balanceAria: `Total: ${total.text}`,
    fiat: total.value && total.fiatAsset ? fiatLabel(total.value, price, currency) : "",
    status: total.reason || "",
  };
}

export function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text != null) node.textContent = text;
  return node;
}

export function walletRequest(base, fallback = "Wallet service unavailable.") {
  return async (path = "", method = "GET", body) => {
    const response = await fetch(base + path, {
      method,
      headers: { "Content-Type": "application/json", ...CSRF_HEADERS },
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
    if (!response.ok) {
      const text = (await response.text()).trim().replace(/[0-9a-f]{64}/gi, "[redacted]").slice(0, 200);
      throw new Error(text || fallback);
    }
    return response.json();
  };
}

export function setMessage(node, text, error = false) {
  node.textContent = text;
  node.classList.toggle("watch-msg-error", error);
}

export function safeLink(href, text) {
  const url = httpsURL(href);
  if (!url) return null;
  const link = el("a", "watch-hint", text);
  link.href = url;
  link.target = "_blank";
  link.rel = "noopener noreferrer";
  return link;
}

function drawSparkline(canvas, values, color) {
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  const dpr = Math.min(window.devicePixelRatio || 1, 2);
  const width = window.innerWidth >= 900 ? 64 : 48;
  const height = 18;
  canvas.width = width * dpr;
  canvas.height = height * dpr;
  canvas.style.width = `${width}px`;
  canvas.style.height = `${height}px`;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  const pts = points(values, width, height);
  if (!pts.length) return;
  ctx.beginPath();
  ctx.moveTo(pts[0].x, pts[0].y);
  for (const point of pts.slice(1)) ctx.lineTo(point.x, point.y);
  ctx.strokeStyle = color;
  ctx.lineWidth = 1.5;
  ctx.lineJoin = "round";
  ctx.stroke();
}

function walletRow(row, view, color) {
  const line = el("div", "ws-row");
  const name = el("span", "ws-name", view.name);
  if (view.nameTitle) {
    name.title = view.nameTitle;
    name.setAttribute("aria-label", view.nameAria);
  }
  line.append(name);
  if (Array.isArray(row.series) && row.series.length >= 2) {
    const spark = el("canvas", "ws-spark");
    spark.title = row.seriesTitle || "Saved balance snapshots";
    spark.setAttribute("aria-label", `${row.label}: ${spark.title}`);
    line.append(spark);
    drawSparkline(spark, row.series, color);
  }
  const balance = el("span", "ws-bal", view.balance);
  balance.setAttribute("aria-label", view.balanceAria);
  if (view.stale) {
    balance.classList.add("stale");
    balance.title = STALE_TITLE;
  }
  line.append(balance);
  if (view.fiat) line.append(el("span", "ws-fiat", view.fiat));
  if (view.status) line.append(el("span", "ws-note", view.status));
  return line;
}

function totalRow(view) {
  const line = el("div", "ws-row ws-total-row");
  const balance = el("span", "ws-bal", view.balance);
  balance.setAttribute("aria-label", view.balanceAria);
  line.append(el("span", "ws-name", "Total"), balance);
  if (view.fiat) line.append(el("span", "ws-fiat", view.fiat));
  if (view.status) line.append(el("span", "ws-note", view.status));
  return line;
}

const rendered = new Set();

// Renders a tower's wallet balances; every chain passes rows in the same shape.
export function renderWalletList(container, rows, options = {}) {
  container.walletList = { rows, options };
  container.rerenderWallets ||= () => renderWalletList(container, container.walletList.rows, container.walletList.options);
  rendered.add(container);
  container.textContent = "";
  container.hidden = rows.length === 0;
  if (!rows.length) return;
  const currency = walletCurrency();
  const color = getComputedStyle(container).color;
  const price = (asset) => (asset ? watchPrice(asset, container.rerenderWallets) : null);
  for (const row of rows) container.append(walletRow(row, rowView(row, price(row.fiatAsset), currency), color));
  if (options.total) container.append(totalRow(totalView(options.total, price(options.total.fiatAsset), currency)));
  container.style.left = window.innerWidth >= WIDE_MIN ? `${towerLayout(window.innerWidth).ox}px` : "";
}

let building = null;

function focusSpot(container) {
  const path = [];
  for (let node = document.activeElement; node && node.parentNode; node = node.parentNode) {
    if (node.parentNode === container) return { key: node.getAttribute?.("data-row-key"), path };
    path.unshift(Array.prototype.indexOf.call(node.parentNode.children, node));
  }
  return null;
}

export function replaceIfChanged(container, key, build) {
  if (container.renderKey === key) return false;
  if (document.querySelector?.("dialog[open]")) return false;
  const spot = focusSpot(container);
  container.expandedKeys ||= new Set();
  building = container.expandedKeys;
  let rows;
  try {
    rows = build();
  } finally {
    building = null;
  }
  container.replaceChildren(...rows);
  container.renderKey = key;
  const present = new Set(rows.map((child) => child.getAttribute?.("data-row-key")));
  container.expandedKeys.forEach((expanded) => present.has(expanded) || container.expandedKeys.delete(expanded));
  const row = spot?.key != null && Array.prototype.find.call(container.children, (child) => child.getAttribute?.("data-row-key") === spot.key);
  row?.contains && spot.path.reduce((node, index) => node?.children?.[index], row)?.focus?.();
  return true;
}

export function managerRow({ label, note = "", keyText = "", canRemove, onRemove, extras = [] }) {
  const row = el("div", "row");
  row.setAttribute("data-row-key", keyText || label);
  const left = el("div", "wk-cell");
  const name = el("span", "label", label);
  if (note) {
    name.title = note;
    name.setAttribute("aria-label", `${label}. ${note}`);
  }
  left.append(name);
  if (keyText) {
    const expandedKeys = building || new Set();
    const key = el("button", "wk");
    key.type = "button";
    const show = () => {
      const open = expandedKeys.has(keyText);
      key.setAttribute("aria-expanded", String(open));
      key.textContent = open ? keyText : shortKey(keyText);
      key.title = open ? "click to collapse" : "click to expand";
    };
    show();
    key.addEventListener("click", () => {
      if (!expandedKeys.delete(keyText)) expandedKeys.add(keyText);
      show();
    });
    left.append(key);
  }
  left.append(...extras.filter(Boolean));
  row.append(left);
  const remove = el("button", "watch-x", "✕");
  remove.type = "button";
  remove.disabled = !canRemove;
  remove.title = `Remove ${label}`;
  remove.setAttribute("aria-label", `Remove ${label}`);
  remove.addEventListener("click", () => {
    askConfirm(`Remove ${label}?`, "Remove").then((yes) => { if (yes) onRemove(); });
  });
  row.append(remove);
  return row;
}

if (typeof window !== "undefined") {
  window.addEventListener("resize", () => rendered.forEach((container) => container.rerenderWallets()));
}
