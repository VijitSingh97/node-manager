import { NODE_LABELS } from "./chains.js";
import { CSRF_HEADERS, dashboardFetch as fetch } from "./session.js";
import { WATCH_COPY, bigSeries, formatAmount, httpsURL, joinNote, scanNote } from "./wallet-format.js";
import { el, managerRow, renderWalletList, replaceIfChanged, safeLink } from "./wallet-ui.js";

const SUPPORTED = new Set(["litecoin", "dogecoin", "bitcoincash", "zcash"]);
const UNITS = { litecoin: "LTC", dogecoin: "DOGE", bitcoincash: "BCH", zcash: "ZEC" };
const PRICE_ASSETS = { litecoin: "litecoin", dogecoin: "dogecoin", bitcoincash: "bitcoin-cash", zcash: "zcash" };
export const SCOPE_NOTES = {
  litecoin: "Balances cover transparent outputs only. Funds inside MWEB are not counted, and the peg-in pool shows as one non-wallet coin.",
};

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
  else if (state === "unscanned") stateText = "Example only; not scanned";
  else if (state === "rescanning") stateText = "Rescanning the chain; this can take hours…";
  else if (ready || stale) stateText = "Balance unavailable";
  else if (state === "error") stateText = WATCH_COPY.scanFailed;
  else if (state === "unavailable") stateText = "Unavailable";
  const history = (Array.isArray(wallet?.history) ? wallet.history : []).slice(-96).map((sample) => familyAmount(sample?.amount).replace(".", ""));
  return {
    label: String(wallet?.label || `${NODE_LABELS[node] || "Chain"} address`),
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
  if (data?.supported !== true) return { canEdit: false, status: reason || "Watch-only balances are unavailable for this chain." };
  if (data?.enabled !== true || data?.active !== true) return { canEdit: false, status: reason || `${NODE_LABELS[node] || "This node"} is unavailable.` };
  if (data?.can_configure !== true && data?.has_password !== true) return { canEdit: false, status: "Open this dashboard from its trusted LAN address to manage watch-only addresses." };
  return { canEdit: true, status: reason };
}

export const familySourceURL = httpsURL;

export function familyWalletPost(node, label, address) {
  return {
    label: String(label || "").trim() || `${NODE_LABELS[node] || "Chain"} wallet`,
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
    const add = el("button", null, WATCH_COPY.add);
    const message = el("p", "family-watch-message");
    let last = { wallets: [] };
    let busy = false;

    label.type = "text";
    label.maxLength = 80;
    label.placeholder = "Label (optional)";
    label.setAttribute("aria-label", `${NODE_LABELS[node]} wallet label`);
    address.type = "text";
    address.maxLength = 160;
    address.placeholder = `${NODE_LABELS[node]} public address`;
    address.autocomplete = "off";
    address.spellcheck = false;
    address.setAttribute("aria-label", `${NODE_LABELS[node]} public address`);
    add.type = "submit";
    form.append(label, address, add, message);
    if (SCOPE_NOTES[node]) root.append(el("p", "watch-hint", SCOPE_NOTES[node]));
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
        const response = await fetch(`/api/${node}/watch/${encodeURIComponent(id)}`, { method: "DELETE", headers: CSRF_HEADERS });
        if (!response.ok) throw new Error(await requestError(response, WATCH_COPY.removeFailed));
        setMessage(WATCH_COPY.removed);
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
      replaceIfChanged(list, JSON.stringify([wallets, access.canEdit, busy]), () => wallets.map((wallet, index) => managerRow({
        label: views[index].label,
        note: views[index].note,
        keyText: String(wallet.address || ""),
        canRemove: access.canEdit && !busy,
        onRemove: () => remove(String(wallet.id || "")),
        extras: wallet.example === true ? [safeLink(wallet.source_url, "Verified public source")] : [],
      })));
    };

    async function refresh(force = false) {
      if (!force && panel?.dataset.nodeActive === "0") return;
      try {
        const response = await fetch(`/api/${node}/watch`, { headers: CSRF_HEADERS });
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
          headers: { ...CSRF_HEADERS, "Content-Type": "application/json" },
          body: JSON.stringify(familyWalletPost(node, label.value, address.value)),
        });
        if (!response.ok) throw new Error(await requestError(response, WATCH_COPY.addFailed));
        label.value = "";
        address.value = "";
        setMessage(WATCH_COPY.added);
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

