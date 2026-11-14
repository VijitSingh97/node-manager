import { dashboardFetch as fetch } from "./session.js";
import { bigSeries, formatAmount, httpsURL, joinNote, scanNote } from "./wallet-format.js";
import { el, managerRow, renderWalletList, safeLink } from "./wallet-ui.js";

const SUPPORTED = new Set(["litecoin", "bitcoincash"]);
const LABELS = { tari: "Tari", litecoin: "Litecoin", dogecoin: "Dogecoin", bitcoincash: "Bitcoin Cash", zcash: "Zcash" };
const UNITS = { litecoin: "LTC", bitcoincash: "BCH" };
const PRICE_ASSETS = { litecoin: "litecoin", bitcoincash: "bitcoin-cash" };
const HEADERS = { "X-Requested-With": "fetch" };

export function familyAmount(value) {
  return typeof value === "string" && /^(0|[1-9]\d*)\.\d{8}$/.test(value) ? value : "";
}

export function familyRow(wallet, node) {
  const amount = familyAmount(wallet?.amount);
  const state = String(wallet?.state || "pending");
  const ready = state === "ok" || state === "ready";
  const stale = state === "stale";
  const shown = (ready || stale) && Boolean(amount);
  let stateText = "Scanning for confirmed funds…";
  if (state === "waiting" || state === "waiting_sync") stateText = "Waiting for node sync…";
  else if (ready || stale) stateText = "Balance unavailable";
  else if (state === "error") stateText = "Scan failed";
  else if (state === "unavailable") stateText = "Unavailable";
  const history = (Array.isArray(wallet?.history) ? wallet.history : []).slice(-96).map((sample) => familyAmount(sample?.amount).replace(".", ""));
  return {
    label: String(wallet?.label || `${LABELS[node] || "Chain"} address`),
    note: wallet?.example === true ? "Public example; excluded from your assets." : "",
    value: shown ? amount : "",
    amount: shown ? formatAmount(amount) : "",
    unit: UNITS[node] || "",
    stateText,
    stale,
    fiatAsset: shown ? PRICE_ASSETS[node] || null : null,
    status: joinNote([stale ? "Cached balance; a fresh scan is unavailable" : "", state === "error" ? "The last scan failed before a balance could be confirmed" : "", scanNote(wallet?.scan_time)]),
    series: bigSeries(history),
    seriesTitle: "Saved balance snapshots",
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

export const familySourceURL = httpsURL;

export function familyWalletPost(node, label, address) {
  return {
    label: String(label || "").trim() || `${LABELS[node] || "Chain"} wallet`,
    address: String(address || "").trim(),
  };
}

if (typeof document !== "undefined") {

  function controller(root) {
    const node = root.dataset.familyWatch;
    const panel = document.getElementById(`${node}-panel`);
    const stats = document.querySelector(`[data-wallet-tower="${node}"]`);
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
      const wallets = data.wallets || [];
      const views = wallets.map((wallet) => familyRow(wallet, node));
      if (stats) renderWalletList(stats, views);
      const rows = wallets.map((wallet, index) => managerRow({
        label: views[index].label,
        note: views[index].note,
        keyText: String(wallet.address || ""),
        canRemove: access.canEdit && !busy,
        onRemove: () => remove(String(wallet.id || "")),
        extras: wallet.example === true ? [safeLink(wallet.source_url, "Verified public source")] : [],
      }));
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

