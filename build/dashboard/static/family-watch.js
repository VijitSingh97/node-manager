import { points } from "./sparkline.js";
import { dashboardFetch as fetch } from "./session.js";

const SUPPORTED = new Set(["litecoin", "bitcoincash"]);
const LABELS = { tari: "Tari", litecoin: "Litecoin", dogecoin: "Dogecoin", bitcoincash: "Bitcoin Cash", zcash: "Zcash" };
const UNITS = { litecoin: "LTC", bitcoincash: "BCH" };
const HEADERS = { "X-Requested-With": "fetch" };

export function familyAmount(value) {
  return typeof value === "string" && /^(0|[1-9]\d*)\.\d{8}$/.test(value) ? value : "";
}

export function familyHistoryPoints(history) {
  const values = (Array.isArray(history) ? history : []).slice(-96)
    .map((sample) => familyAmount(sample?.amount))
    .filter((amount) => amount && amount.length <= 40)
    .map((amount) => BigInt(amount.replace(".", "")));
  if (values.length < 2) return [];
  const minimum = values.reduce((left, right) => left < right ? left : right);
  return points(values.map((amount) => Number(amount - minimum)), 160, 28);
}

export function familyWalletView(wallet, node) {
  const unit = UNITS[node] || "";
  const amount = familyAmount(wallet?.amount);
  const state = String(wallet?.state || "pending");
  const ready = state === "ok" || state === "ready";
  const stale = state === "stale";
  let balance = "Scanning for confirmed funds…";
  let note = "A complete UTXO set scan is pending.";
  if (state === "waiting" || state === "waiting_sync") {
    balance = "Waiting for node sync…";
    note = "The scan starts after the node is ready.";
  } else if (ready) {
    balance = amount ? `${amount} ${unit}` : "Balance unavailable";
    note = amount ? "Confirmed UTXOs" : "The node returned an invalid balance.";
  } else if (stale) {
    balance = amount ? `${amount} ${unit}` : "Balance unavailable";
    note = amount ? "Cached balance; a fresh scan is unavailable." : "A cached balance is unavailable.";
  } else if (state === "error") {
    balance = "Scan failed";
    note = "The last scan failed before a balance could be confirmed.";
  } else if (state === "unavailable") {
    balance = "Unavailable";
    note = "Accurate watch-only balances are unavailable for this node.";
  }
  const height = Number.isSafeInteger(wallet?.scan_height) && wallet.scan_height > 0 ? `Block ${wallet.scan_height}` : "";
  let scanned = typeof wallet?.scan_time === "string" ? wallet.scan_time : "";
  if (Number.isSafeInteger(wallet?.scan_time) && wallet.scan_time > 0) {
    const date = new Date(wallet.scan_time * 1000);
    if (Number.isFinite(date.getTime())) scanned = date.toLocaleString();
  }
  const scanTime = scanned ? `Scanned ${scanned}` : "";
  const example = wallet?.example === true;
  return {
    label: String(wallet?.label || `${LABELS[node] || "Chain"} address`),
    address: String(wallet?.address || ""),
    balance,
    note: [example ? "Public example; excluded from your assets." : "", note, height, scanTime].filter(Boolean).join(" "),
    example,
    stale,
  };
}

export function familyAccessView(data, node) {
  const reason = String(data?.reason || "");
  if (data?.supported !== true && (node === "dogecoin" || node === "zcash")) return { canEdit: false, status: "Wallet balance not available." };
  if (data?.supported !== true) return { canEdit: false, status: reason || "Watch-only balances are unavailable for this chain." };
  if (data?.enabled !== true || data?.active !== true) return { canEdit: false, status: reason || `${LABELS[node] || "This node"} is unavailable.` };
  if (data?.can_configure !== true && data?.has_password !== true) return { canEdit: false, status: "Open this dashboard from its trusted LAN address to manage watch-only addresses." };
  return { canEdit: true, status: reason };
}

export function familySourceURL(value) {
  try {
    const url = new URL(String(value || ""));
    return url.protocol === "https:" ? url.href : "";
  } catch {
    return "";
  }
}

export function familyWalletPost(node, label, address) {
  return {
    label: String(label || "").trim() || `${LABELS[node] || "Chain"} wallet`,
    address: String(address || "").trim(),
  };
}

if (typeof document !== "undefined") {
  const el = (tag, className, text) => {
    const element = document.createElement(tag);
    if (className) element.className = className;
    if (text !== undefined) element.textContent = text;
    return element;
  };

  function controller(root) {
    const node = root.dataset.familyWatch;
    const panel = document.getElementById(`${node}-panel`);
    const status = root.querySelector("[data-family-watch-status]");
    const list = root.querySelector("[data-family-watch-list]");
    const form = el("form", "family-watch-form");
    const label = el("input");
    const address = el("input");
    const add = el("button", null, "Add public address");
    const message = el("p", "family-watch-message");
    let last = { wallets: [] };
    let busy = false;

    label.type = "text";
    label.maxLength = 80;
    label.placeholder = "Label (optional)";
    label.setAttribute("aria-label", `${LABELS[node]} wallet label`);
    address.type = "text";
    address.maxLength = 160;
    address.placeholder = `${LABELS[node]} public address`;
    address.autocomplete = "off";
    address.spellcheck = false;
    address.setAttribute("aria-label", `${LABELS[node]} public address`);
    add.type = "submit";
    form.append(label, address, add, message);
    if (SUPPORTED.has(node)) root.append(form);

    const setMessage = (text, error = false) => {
      message.textContent = text;
      message.classList.toggle("family-watch-error", error);
    };

    const requestError = async (response, fallback) => (await response.text()).trim().slice(0, 200) || fallback;

    const remove = async (id) => {
      if (busy) return;
      busy = true;
      setMessage("");
      try {
        const response = await fetch(`/api/${node}/watch/${encodeURIComponent(id)}`, { method: "DELETE", headers: HEADERS });
        if (!response.ok) throw new Error(await requestError(response, "Address could not be removed."));
        setMessage("Address removed.");
        await refresh(true);
      } catch (error) {
        setMessage(error.message || "Network error.", true);
      } finally {
        busy = false;
        add.disabled = !familyAccessView(last, node).canEdit;
      }
    };

    const render = (data) => {
      last = data;
      const access = familyAccessView(data, node);
      status.classList.remove("family-watch-error");
      form.hidden = !access.canEdit;
      add.disabled = busy || !access.canEdit;
      status.textContent = access.status || (!data.wallets?.length ? "No watch-only addresses." : "");
      const rows = [];
      for (const wallet of data.wallets || []) {
        const view = familyWalletView(wallet, node);
        const row = el("div", "family-watch-row");
        const heading = el("div", "family-watch-heading");
        const name = el("strong", null, view.label);
        const balance = el("span", `family-watch-balance${view.stale ? " stale" : ""}`, view.balance);
        heading.append(name, balance);
        const publicAddress = el("code", "family-watch-address", view.address);
        const note = el("p", "family-watch-note", view.note);
        row.append(heading, publicAddress, note);
        const history = familyHistoryPoints(wallet.history);
        if (history.length) {
          const chart = document.createElementNS("http://www.w3.org/2000/svg", "svg");
          const line = document.createElementNS("http://www.w3.org/2000/svg", "polyline");
          chart.classList.add("family-watch-history");
          chart.setAttribute("viewBox", "0 0 160 28");
          chart.setAttribute("preserveAspectRatio", "none");
          chart.setAttribute("role", "img");
          chart.setAttribute("aria-label", "Saved balance snapshots");
          line.setAttribute("points", history.map(({ x, y }) => `${x},${y}`).join(" "));
          line.setAttribute("fill", "none");
          line.setAttribute("stroke", "currentColor");
          line.setAttribute("stroke-width", "1.5");
          line.setAttribute("vector-effect", "non-scaling-stroke");
          chart.append(line);
          row.append(chart);
        }
        const source = view.example ? familySourceURL(wallet.source_url) : "";
        if (source) {
          const link = el("a", "family-watch-source", "Verified public source");
          link.href = source;
          link.target = "_blank";
          link.rel = "noreferrer";
          row.append(link);
        }
        if (access.canEdit) {
          const button = el("button", "family-watch-remove", `Remove ${view.label}`);
          button.type = "button";
          button.addEventListener("click", () => remove(String(wallet.id || "")));
          row.append(button);
        }
        rows.push(row);
      }
      list.replaceChildren(...rows);
    };

    async function refresh(force = false) {
      if (!force && panel?.dataset.nodeActive === "0") return;
      try {
        const response = await fetch(`/api/${node}/watch`, { headers: HEADERS });
        if (!response.ok) throw new Error(await requestError(response, "Watch-only addresses could not be refreshed."));
        render(await response.json());
      } catch (error) {
        status.textContent = error.message || "Watch-only addresses could not be refreshed.";
        status.classList.add("family-watch-error");
      }
    }

    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const access = familyAccessView(last, node);
      if (busy || !access.canEdit) return;
      busy = true;
      add.disabled = true;
      setMessage("");
      try {
        const response = await fetch(`/api/${node}/watch`, {
          method: "POST",
          headers: { ...HEADERS, "Content-Type": "application/json" },
          body: JSON.stringify(familyWalletPost(node, label.value, address.value)),
        });
        if (!response.ok) throw new Error(await requestError(response, "Address could not be added."));
        label.value = "";
        address.value = "";
        setMessage("Address added; scanning confirmed UTXOs.");
        await refresh(true);
      } catch (error) {
        setMessage(error.message || "Network error.", true);
      } finally {
        busy = false;
        add.disabled = !familyAccessView(last, node).canEdit;
      }
    });

    refresh();
    return { node, refresh };
  }

  const controllers = [...document.querySelectorAll("[data-family-watch]")].map(controller);
  const refreshNode = (node) => controllers.find((item) => item.node === node)?.refresh();
  window.addEventListener("node-selected", (event) => refreshNode(event.detail?.node));
  window.addEventListener("node-status-updated", (event) => refreshNode(event.detail?.node));
  setInterval(() => controllers.forEach((item) => item.refresh()), 30000);
}

