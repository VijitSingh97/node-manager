import { atomicToDecimal, bigSeries, formatAmount, joinNote, scanNote } from "./wallet-format.js";
import { el, managerRow, renderWalletList, setMessage as showMessage, walletRequest } from "./wallet-ui.js";

const HEX_KEY = /^[0-9a-fA-F]{64}$/;
const BIRTHDAY_MIN = "2022-01-01";

function todayISO() {
  const date = new Date();
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}

function validBirthday(value, today) {
  if (value === "") return true;
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value) || value < BIRTHDAY_MIN || value > today) return false;
  const [year, month, day] = value.split("-").map(Number);
  const date = new Date(Date.UTC(year, month - 1, day));
  return date.getUTCFullYear() === year && date.getUTCMonth() === month - 1 && date.getUTCDate() === day;
}

export function tariWalletImport(value, today = todayISO()) {
  const keys = ["name", "public_spend_key", "view_key", "birthday"];
  if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some((key) => !keys.includes(key))) {
    throw new Error("Use view-only JSON with public_spend_key, view_key, and optional name and birthday. Seeds and private spend keys are not accepted.");
  }
  if (value.name !== undefined && typeof value.name !== "string") throw new Error("Wallet name must be text.");
  const name = value.name?.trim() || "Tari wallet";
  const publicSpendKey = typeof value.public_spend_key === "string" ? value.public_spend_key.trim() : "";
  const viewKey = typeof value.view_key === "string" ? value.view_key.trim() : "";
  if (value.birthday !== undefined && typeof value.birthday !== "string") throw new Error("Birthday must be a date in YYYY-MM-DD format.");
  const birthday = typeof value.birthday === "string" ? value.birthday.trim() : "";
  if (name.length > 80) throw new Error("Wallet name must be 80 characters or fewer.");
  if (!HEX_KEY.test(publicSpendKey)) throw new Error("A 64-character public spend key is required.");
  if (!HEX_KEY.test(viewKey)) throw new Error("A 64-character private view key is required.");
  if (!validBirthday(birthday, today)) throw new Error(`Birthday must be blank or a valid date from ${BIRTHDAY_MIN} through ${today}.`);
  return { name, public_spend_key: publicSpendKey, view_key: viewKey, birthday };
}

export function tariDraftAfterSubmit(draft, accepted) {
  if (accepted) return { name: "", public_spend_key: "", view_key: "", birthday: "" };
  return { ...draft, view_key: "" };
}

export function tariAccessView(data) {
  const reason = typeof data?.reason === "string" ? data.reason : "";
  if (data?.can_configure !== true && data?.has_password !== true) return { canEdit: false, status: "Open this dashboard from its trusted LAN address to use view-only wallets." };
  if (data?.active !== true) return { canEdit: false, status: reason || "Tari is inactive." };
  if (data?.available !== true) return { canEdit: false, status: reason || "Tari wallet service unavailable." };
  return { canEdit: true, status: reason };
}

const xtm = (atomic) => atomicToDecimal(atomic, 6);

export function tariRow(wallet) {
  const state = String(wallet?.state || "pending");
  const decimal = xtm(wallet?.balance_atomic);
  const stale = state === "stale";
  const verified = state === "ok" || (stale && Number.isSafeInteger(wallet?.scan_time) && wallet.scan_time > 0);
  const shown = verified && decimal !== null;
  let stateText = "Pending scan…";
  if (state === "waiting") stateText = "Waiting for node sync…";
  else if (state === "removing") stateText = "Removing…";
  else if (state === "error") stateText = "Scan failed";
  else if (verified || stale) stateText = "Balance unavailable";
  const status = [];
  if (state === "pending" || state === "waiting") {
    const scanned = Number(wallet?.scan_height);
    const target = Number(wallet?.target_height);
    if (Number.isSafeInteger(scanned) && Number.isSafeInteger(target) && scanned >= 0 && target > 0) {
      status.push(`Scanning · ${Math.min(100, 100 * scanned / target).toFixed(1)}%`);
    }
  }
  if (stale) status.push(verified ? "Saved confirmed balance; waiting for a fresh scan" : "Waiting for the first verified scan");
  if (state === "error") status.push("The last scan failed before a current balance could be confirmed");
  status.push(scanNote(wallet?.scan_time));
  const history = (Array.isArray(wallet?.history) ? wallet.history : []).slice(-96).map((sample) => sample?.balance_atomic);
  return {
    label: String(wallet?.name || "Tari wallet"),
    note: "",
    value: shown ? decimal : "",
    amount: shown ? formatAmount(decimal) : "",
    unit: "XTM",
    stateText,
    stale,
    fiatAsset: shown ? "tari" : null,
    status: joinNote(status),
    series: bigSeries(history),
    seriesTitle: "Saved Tari balance snapshots",
  };
}

export function tariDetails(wallet) {
  const details = [];
  if (tariRow(wallet).amount) {
    for (const [label, field] of [
      ["Available", "available_atomic"],
      ["Timelocked", "timelocked_atomic"],
      ["Pending incoming", "pending_incoming_atomic"],
      ["Pending outgoing", "pending_outgoing_atomic"],
    ]) {
      const value = xtm(wallet?.[field]);
      if (value !== null) details.push({ label, value: `${formatAmount(value)} XTM` });
    }
  }
  if (wallet?.birthday) details.push({ label: "Birthday", value: String(wallet.birthday) });
  return details;
}

if (typeof document !== "undefined") {
  const card = document.getElementById("tari-watch");
  const stats = document.querySelector('[data-wallet-tower="tari"]');
  if (card && stats) {
    const request = walletRequest("/api/tari/watch", "Tari wallet service unavailable.");
    const heading = el("h2", null, "Watch-only wallet");
    const status = el("p", "node-state", "Loading wallet service…");
    status.setAttribute("role", "status");
    const list = el("div", "watch-list");
    const form = el("form", "watch-add tari-watch-form");
    const input = (label, type = "text", maxLength = 100) => {
      const node = el("input");
      node.type = type;
      node.placeholder = label;
      node.setAttribute("aria-label", label);
      node.autocomplete = "off";
      node.maxLength = maxLength;
      return node;
    };
    const name = input("Wallet name (optional)", "text", 80);
    const publicSpendKey = input("Public spend key", "text", 64);
    const viewKey = input("Private view key", "password", 64);
    viewKey.autocomplete = "new-password";
    const birthday = input("Wallet birthday (optional)", "date");
    birthday.min = BIRTHDAY_MIN;
    birthday.max = todayISO();
    publicSpendKey.required = viewKey.required = true;
    const scanOptions = el("details");
    scanOptions.append(el("summary", null, "Scan options (optional)"), birthday);
    const fileLabel = el("label", "watch-hint", "Or upload view-only wallet JSON");
    const file = input("Tari view-only wallet JSON", "file");
    file.accept = ".json,application/json";
    fileLabel.append(file);
    const add = el("button", null, "Add wallet");
    add.type = "submit";
    const hint = el("p", "watch-hint", "The private view key reveals funds but cannot spend them. The spend key entered here is public. Leave the birthday blank to scan from genesis. Never enter a seed or private spend key.");
    const message = el("p", "watch-msg");
    message.setAttribute("role", "status");
    form.append(name, publicSpendKey, viewKey, scanOptions, fileLabel, add);
    card.replaceChildren(heading, status, list, form, hint, message);
    let last = { wallets: [] };
    let busy = false;

    const setMessage = (text, error = false) => showMessage(message, text, error);
    const setDraft = (draft) => {
      name.value = draft.name;
      publicSpendKey.value = draft.public_spend_key;
      viewKey.value = draft.view_key;
      birthday.value = draft.birthday;
    };
    const currentDraft = () => ({ name: name.value, public_spend_key: publicSpendKey.value, view_key: viewKey.value, birthday: birthday.value });
    function render(data) {
      last = data;
      const access = tariAccessView(data);
      const wallet = Array.isArray(data.wallets) ? data.wallets[0] : null;
      const queued = wallet?.state === "removing";
      form.hidden = Boolean(wallet) || !access.canEdit;
      for (const field of [name, publicSpendKey, viewKey, birthday, file, add]) field.disabled = busy || !access.canEdit || Boolean(wallet);
      status.textContent = access.status || (wallet ? "" : "No view-only wallet configured.");
      status.classList.remove("watch-msg-error");
      list.replaceChildren();
      renderWalletList(stats, wallet ? [tariRow(wallet)] : []);
      if (!wallet) return;
      const row = tariRow(wallet);
      const details = el("div", "tari-wallet-metrics");
      for (const detail of tariDetails(wallet)) {
        const metric = el("div", "row");
        metric.append(el("span", "label", `${detail.label}:`), el("span", null, detail.value));
        details.append(metric);
      }
      list.append(managerRow({
        label: row.label,
        keyText: String(wallet.public_spend_key || ""),
        canRemove: !busy && access.canEdit && !queued,
        onRemove: async () => {
          if (busy) return;
          busy = true;
          render(last);
          setMessage("Removing wallet…");
          try {
            await request(`/${encodeURIComponent(String(wallet.id || ""))}`, "DELETE");
            setMessage("Wallet removal queued.");
            await refresh(true);
          } catch (error) {
            setMessage(error.message || "Wallet could not be removed.", true);
          } finally {
            busy = false;
            render(last);
          }
        },
        extras: details.childElementCount ? [details] : [],
      }));
    }
    async function refresh(force = false) {
      const panel = document.getElementById("tari-panel");
      if (!force && panel?.dataset.nodeActive === "0") return;
      try {
        const data = await request();
        render(data);
      } catch (error) {
        status.textContent = error.message || "Tari wallet service unavailable.";
        status.classList.add("watch-msg-error");
      }
    }
    file.addEventListener("change", async () => {
      try {
        const selected = file.files[0];
        if (!selected) return;
        if (selected.size > 16384) throw new Error("View-only wallet JSON is too large.");
        setDraft(tariWalletImport(JSON.parse(await selected.text()), birthday.max));
        setMessage("View-only details loaded. Choose Add wallet to start scanning.");
      } catch (error) {
        viewKey.value = "";
        setMessage(error instanceof SyntaxError ? "Choose a valid JSON file." : error.message, true);
      } finally {
        file.value = "";
      }
    });
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (busy || !tariAccessView(last).canEdit || last.wallets?.length || !form.reportValidity()) return;
      busy = true;
      add.disabled = true;
      const draft = currentDraft();
      let accepted = false;
      try {
        const payload = tariWalletImport(draft, birthday.max);
        await request("", "POST", payload);
        accepted = true;
        viewKey.value = "";
        setMessage("Wallet accepted. Scanning will continue in the background.");
        await refresh(true);
      } catch (error) {
        setMessage(error.message || "Wallet could not be added.", true);
      } finally {
        setDraft(tariDraftAfterSubmit(draft, accepted));
        busy = false;
        render(last);
      }
    });
    window.addEventListener("node-selected", (event) => {
      if (event.detail?.node === "tari") refresh();
      else viewKey.value = "";
    });
    window.addEventListener("node-status-updated", (event) => { if (event.detail?.node === "tari") refresh(); });
    refresh();
    setInterval(refresh, 15000);
  }
}
