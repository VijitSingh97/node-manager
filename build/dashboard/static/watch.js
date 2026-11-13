import { dashboardFetch as fetch } from "./session.js";
import { formatAmount, scanNote as scannedAt } from "./wallet-format.js";
import { el, managerRow, renderWalletList } from "./wallet-ui.js";

const CSRF = { "X-Requested-With": "fetch" }; // blocks cross-site form posts
const POLL_MS = 15000;
const GENESIS_ADDRESS = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa";

export function balanceLabel(w) {
  if (w.state === "pending" || w.state === "scanning") {
    return w.balance_kind === "confirmed" ? "Pending balance…" : "Pending history…";
  }
  if (w.state === "ok" || w.state === "stale") return `${formatAmount(w.btc)} BTC`;
  return "—"; // error / not ready
}

export function totalLabel(view) {
  if (view.total_state === "pending") {
    return view.wallets?.some(w => w.balance_kind === "confirmed")
      ? "Pending balance…" : "Pending history…";
  }
  if (view.total_state === "unavailable") return "—";
  return `${formatAmount(view.total)} BTC`;
}

export function scanNote(w) {
  return w.balance_kind === "confirmed" ? scannedAt(w.scan_time) : "";
}

export function btcRow(w) {
  const ready = w.state === "ok" || w.state === "stale";
  const chainSeries = Array.isArray(w.chain_history) && w.chain_history.length >= 2;
  return {
    label: walletView(w).label,
    note: publicExampleNote(w) || genesisNote(w),
    value: ready ? w.btc : "",
    amount: ready ? formatAmount(w.btc) : "",
    unit: "BTC",
    stateText: balanceLabel(w),
    stale: w.state === "stale",
    fiatAsset: ready ? "bitcoin" : null,
    status: scanNote(w),
    series: chainSeries ? w.chain_history : w.history,
    seriesTitle: chainSeries ? "Confirmed unspent funds by arrival block, first seen to the scan tip" : "Saved balance snapshots; not transaction history",
  };
}

export function btcTotal(view) {
  const reasons = {
    mixed_sources: "Wallet sources may overlap; a combined total is unavailable.",
    scan_cache_unavailable: "Saved balance cache unavailable; a current total cannot be verified.",
  };
  return {
    text: totalLabel(view),
    value: view.total_state === "ok" ? view.total : "",
    fiatAsset: "bitcoin",
    reason: view.total_reason ? reasons[view.total_reason] || "Wallet outputs could not be combined reliably; showing individual balances." : "",
  };
}

export function watchHint(pruned) {
  return pruned
    ? "Public information only; never enter a seed or private key. The first balance scan may take minutes. Results cover confirmed funds in the scanned range, without mempool transactions or full transaction history."
    : "Public information only; never enter a seed or private key. For a bare xpub/tpub, choose its wallet's address type. Set the birthday to shorten the first rescan.";
}

export function walletView(w) {
  return { label: w.example ? w.name : w.display_name || w.name, deleteName: w.name };
}

export function publicExampleNote(w) {
  return w.example
    ? "Public example: one disclosed Binance address, excluded from your totals; it is not Binance's total reserves."
    : "";
}

export function genesisNote(w) {
  return w.key === GENESIS_ADDRESS
    ? "Bitcoin Core balance excludes the unspendable 50 BTC genesis reward"
    : "";
}

export function editAccess(view) {
  const enabled = view.can_configure === true || view.has_password === true;
  return {
    enabled,
    warning: enabled ? "" : "Open this dashboard from its trusted LAN address to manage watch-only wallets.",
  };
}

export function isBareExtendedPublicKey(key) {
  return /^(xpub|tpub)[1-9A-HJ-NP-Za-km-z]{107}$/.test(String(key || "").trim());
}

export function walletKeyForPost(key, scriptType = "legacy") {
  const publicKey = String(key || "").trim();
  if (!isBareExtendedPublicKey(publicKey)) return publicKey;
  if (scriptType === "native") return `wpkh(${publicKey}/<0;1>/*)`;
  if (scriptType === "nested") return `sh(wpkh(${publicKey}/<0;1>/*))`;
  return publicKey;
}

export function walletPost(name, key, scriptType, birthday, wallets = []) {
  let walletName = String(name || "").trim();
  if (!walletName) {
    const identifier = value => [...String(value || "")].slice(0, 40)
      .map(char => /[\p{L}\p{N}_-]/u.test(char) ? char : "_").join("");
    const names = new Set(wallets.map(wallet => String(wallet?.name || "").toLocaleLowerCase()));
    const identifiers = new Set(wallets.map(wallet => identifier(wallet?.name)));
    for (let number = 1; !walletName; number += 1) {
      const candidate = `Wallet ${number}`;
      if (!names.has(candidate.toLocaleLowerCase()) && !identifiers.has(identifier(candidate))) walletName = candidate;
    }
  }
  return { name: walletName, key: walletKeyForPost(key, scriptType), birthday: String(birthday || "") };
}

if (typeof document !== "undefined") {
  const card = document.getElementById("watch");
  const stats = document.getElementById("tower-stats");


  // --- manager card: heading, wallet list, add form, warning ---
  const list = el("div", "watch-list");
  const msg = el("div", "watch-msg");
  const warn = el("div", "watch-warn");
  let editingEnabled = false;

  const form = el("form", "watch-add");
  const name = el("input");
  name.placeholder = "Label (optional)";
  name.maxLength = 40;
  name.setAttribute("aria-label", "Wallet label");
  const key = el("input");
  key.placeholder = "xpub / zpub / address / public descriptor";
  key.setAttribute("aria-label", "Public wallet information: xpub, zpub, address, or public descriptor");
  const scriptType = el("select", "watch-script");
  scriptType.setAttribute("aria-label", "Bare xpub address type");
  for (const [value, text] of [
    ["legacy", "Bare xpub: Legacy (pkh)"],
    ["native", "Bare xpub: Native SegWit (wpkh)"],
    ["nested", "Bare xpub: Nested SegWit (sh-wpkh)"],
  ]) {
    const option = el("option", null, text);
    option.value = value;
    scriptType.append(option);
  }
  scriptType.disabled = true;
  key.addEventListener("input", () => {
    scriptType.disabled = !isBareExtendedPublicKey(key.value);
  });
  const bday = el("input");
  bday.type = "date";
  bday.max = new Date().toISOString().slice(0, 10);
  bday.title = "Wallet birthday (optional) — bounds the first rescan";
  bday.setAttribute("aria-label", "Wallet birthday (optional)");
  const add = el("button", null, "Add");
  add.type = "submit";
  form.append(name, key, scriptType, bday, add);
  const hint = el("div", "watch-hint", watchHint(false));

  card.append(el("h2", null, "Watch-only wallets"), list, form, hint, msg, warn);

  const renderManager = (view) => {
    const access = editAccess(view);
    editingEnabled = access.enabled;
    add.disabled = !editingEnabled;
    bday.disabled = !!view.pruned;
    bday.hidden = !!view.pruned;
    if (view.pruned) bday.value = "";
    hint.textContent = watchHint(!!view.pruned);
    list.textContent = "";
    for (const w of view.wallets) {
      const identity = walletView(w);
      list.append(managerRow({
        label: identity.label,
        note: publicExampleNote(w),
        keyText: w.key,
        canRemove: editingEnabled,
        onRemove: () => remove(identity.deleteName),
      }));
    }
    warn.textContent = [access.warning, view.scan_cache_unavailable
      ? "Saved balance cache unavailable. Check storage permissions and restart the dashboard."
      : ""].filter(Boolean).join(" ");
  };

  const renderStats = (view) => {
    renderWalletList(stats, view.wallets.map(btcRow), { total: view.show_total ? btcTotal(view) : null });
  };

  let last = { wallets: [] };
  let loadFailed = false;
  const render = (view) => { last = view; renderManager(view); renderStats(view); };


  async function refresh() {
    try {
      const res = await fetch("/api/watch", { headers: CSRF });
      if (res.ok) {
        render(await res.json());
        if (loadFailed) setMsg("");
        loadFailed = false;
      } else {
        loadFailed = true;
        setMsg(await res.text(), true);
      }
    } catch { /* blip — next poll */ }
  }

  // success/"scanning" text stays the default muted color; errors get a
  // distinct (warn/red) class so a failed add doesn't read the same as one
  // in progress.
  const setMsg = (text, isError) => {
    msg.textContent = text;
    msg.classList.toggle("watch-msg-error", !!isError);
  };

  async function remove(walletName) {
    setMsg("");
    try {
      const res = await fetch(`/api/watch/${encodeURIComponent(walletName)}`,
                             { method: "DELETE", headers: CSRF });
      if (!res.ok) setMsg(await res.text(), true);
    } catch { setMsg("Network error.", true); }
    refresh();
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!editingEnabled) return;
    setMsg("");
    add.disabled = true;
    try {
      const res = await fetch("/api/watch", {
        method: "POST",
        headers: { ...CSRF, "Content-Type": "application/json" },
        body: JSON.stringify(walletPost(name.value, key.value, scriptType.value, bday.value, last.wallets)),
      });
      if (res.ok) {
        name.value = key.value = bday.value = "";
        scriptType.value = "legacy";
        scriptType.disabled = true;
        setMsg(last.pruned
          ? "Added — scanning for confirmed funds. This may take a few minutes…"
          : "Added — scanning the chain for its history…");
      } else {
        setMsg(await res.text(), true);
      }
    } catch { setMsg("Network error.", true); }
    add.disabled = !editingEnabled;
    refresh();
  });

  card.hidden = false;
  refresh();
  setInterval(refresh, POLL_MS);
}
