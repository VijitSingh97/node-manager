// Watch-only wallets. The card (#watch) is the manager — each wallet's label +
// a truncated key you can click to expand, a remove ✕, and the add form. The
// balances themselves render in gold above the tower (#tower-stats): per-wallet,
// plus a total once there's more than one. Talks to /api/watch.
//
// Lives outside #live so the 30s status refresh never clobbers the add form.
// Every bit of user data (labels, keys) goes in via textContent — no XSS.
import { towerLayout, WIDE_MIN } from "./tower.js";
import { points } from "./sparkline.js";
import { dashboardFetch as fetch } from "./session.js";

const CSRF = { "X-Requested-With": "fetch" }; // blocks cross-site form posts
const POLL_MS = 15000;
const GENESIS_ADDRESS = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa";

export function balanceLabel(w) {
  if (w.state === "pending" || w.state === "scanning") {
    return w.balance_kind === "confirmed" ? "Pending balance…" : "Pending history…";
  }
  if (w.state === "ok" || w.state === "stale") return `${w.btc} BTC`;
  return "—"; // error / not ready
}

export function totalLabel(view) {
  if (view.total_state === "pending") {
    return view.wallets?.some(w => w.balance_kind === "confirmed")
      ? "Pending balance…" : "Pending history…";
  }
  if (view.total_state === "unavailable") return "—";
  return `${view.total} BTC`;
}

export function scanNote(w) {
  if (w.balance_kind !== "confirmed") return "";
  const parts = [w.state === "stale" ? "Last confirmed scan · updating" : "Confirmed balance"];
  if (Number.isInteger(w.range?.start) && Number.isInteger(w.range?.end)) {
    parts.push(`derived indexes ${w.range.start}–${w.range.end}`);
  }
  if (Number.isInteger(w.scan_height)) parts.push(`block ${w.scan_height}`);
  if (w.scan_time && Number.isFinite(Date.parse(w.scan_time))) {
    parts.push(`scanned ${new Date(w.scan_time).toLocaleString()}`);
  }
  return parts.join(" · ");
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

// first 4 … last 4 of a key/address/descriptor; full string if it's short.
export function shortKey(k) {
  return k.length > 12 ? `${k.slice(0, 4)}…${k.slice(-4)}` : k;
}

if (typeof document !== "undefined") {
  const card = document.getElementById("watch");
  const stats = document.getElementById("tower-stats");

  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  };

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
      const row = el("div", "row");
      const left = el("div", "wk-cell");
      const walletLabel = el("span", "label", identity.label);
      const note = publicExampleNote(w);
      if (note) {
        walletLabel.title = note;
        walletLabel.setAttribute("aria-label", `${identity.label}. ${note}`);
      }
      left.append(walletLabel);
      // truncated key, click to expand/collapse
      const k = el("span", "wk", shortKey(w.key));
      k.title = "click to expand";
      k.addEventListener("click", () => {
        k.textContent = k.textContent === w.key ? shortKey(w.key) : w.key;
      });
      left.append(k);
      row.append(left);
      const x = el("button", "watch-x", "✕");
      x.type = "button";
      x.disabled = !editingEnabled;
      x.title = `Remove ${identity.label}`;
      x.setAttribute("aria-label", `Remove ${identity.label}`);
      x.addEventListener("click", () => remove(identity.deleteName));
      row.append(x);
      list.append(row);
    }
    warn.textContent = [access.warning, view.scan_cache_unavailable
      ? "Saved balance cache unavailable. Check storage permissions and restart the dashboard."
      : ""].filter(Boolean).join(" ");
  };

  // --- gold balances above the tower ---
  const renderStats = (view) => {
    stats.textContent = "";
    if (!view.wallets.length) { stats.hidden = true; return; }
    stats.hidden = false;
    // three cells per row — label | sparkline | balance — placed into shared
    // grid columns (CSS) so they line up across every row regardless of width.
    for (const w of view.wallets) {
      const row = el("div", "ws-row");
      const identity = walletView(w);
      const walletName = el("span", "ws-name", identity.label);
      const note = publicExampleNote(w) || genesisNote(w);
      if (note) {
        walletName.textContent += " ⓘ";
        walletName.title = note;
        walletName.setAttribute("aria-label", `${identity.label}. ${note}`);
      }
      row.append(walletName);
      if (w.history && w.history.length >= 2) {
        const spark = document.createElement("canvas");
        spark.className = "ws-spark";
        spark.title = "Saved balance snapshots; not transaction history";
        spark.setAttribute("aria-label", `${identity.label}: saved balance snapshots`);
        row.append(spark);
        drawSpark(spark, w.history);
      }
      const bal = el("span", "ws-bal", balanceLabel(w));
      bal.setAttribute("aria-label", `${identity.label}: ${balanceLabel(w)}`);
      if (w.state === "stale") {
        bal.classList.add("stale");
        bal.title = "Last known balance; waiting for a fresh result";
      }
      row.append(bal);
      const scan = scanNote(w);
      if (scan) row.append(el("span", "ws-note", scan));
      stats.append(row);
    }
    if (view.show_total) {
      const t = el("div", "ws-row ws-total-row");
      const total = totalLabel(view);
      const totalBalance = el("span", "ws-bal", total);
      totalBalance.setAttribute("aria-label", `Total: ${total}`);
      t.append(el("span", "ws-name", "Total"), totalBalance);
      if (view.total_reason) {
        const reason = view.total_reason === "mixed_sources"
          ? "Wallet sources may overlap; a combined total is unavailable."
          : view.total_reason === "scan_cache_unavailable"
            ? "Saved balance cache unavailable; a current total cannot be verified."
            : "Wallet outputs could not be combined reliably; showing individual balances.";
        t.append(el("span", "ws-note", reason));
      }
      stats.append(t);
    }
    placeStats();
  };

  // a tiny gold sparkline of a wallet's persisted balance history
  function drawSpark(canvas, values) {
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    // wider line on desktop; compact on phones so a long name + big balance
    // still fit on the same row
    const w = window.innerWidth >= 900 ? 100 : 48, h = 18;
    canvas.width = w * dpr;
    canvas.height = h * dpr;
    canvas.style.width = w + "px";
    canvas.style.height = h + "px";
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    const pts = points(values, w, h);
    if (!pts.length) return;
    ctx.beginPath();
    ctx.moveTo(pts[0].x, pts[0].y);
    for (let i = 1; i < pts.length; i++) ctx.lineTo(pts[i].x, pts[i].y);
    ctx.strokeStyle = getComputedStyle(document.documentElement)
      .getPropertyValue("--accent").trim() || "#f2a900";
    ctx.lineWidth = 1.5;
    ctx.lineJoin = "round";
    ctx.stroke();
  }

  // On desktop the balances float above the tower, centred on its axis; JS only
  // sets the horizontal centre (the CSS pins the top). On mobile they sit in the
  // normal flow at the top of the column.
  function placeStats() {
    if (window.innerWidth >= WIDE_MIN) {
      stats.style.left = towerLayout(window.innerWidth).ox + "px";
    } else {
      stats.style.left = "";
    }
  }
  let last = { wallets: [] };
  let loadFailed = false;
  const render = (view) => { last = view; renderManager(view); renderStats(view); };
  // re-render on resize so the block re-centres and the sparklines redraw at the
  // width for the new breakpoint
  window.addEventListener("resize", () => renderStats(last));

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
