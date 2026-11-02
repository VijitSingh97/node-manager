import { points } from "./sparkline.js";
import { towerLayout, WIDE_MIN } from "./tower.js";
import { dashboardFetch as fetch } from "./session.js";

const API = "/api/monero/watch";
const HEADERS = { "Content-Type": "application/json", "X-Requested-With": "fetch" };

export function formatXMR(atomic) {
  if (typeof atomic !== "string" || !/^\d+$/.test(atomic)) return "—";
  const value = BigInt(atomic);
  return `${value / 1000000000000n}.${String(value % 1000000000000n).padStart(12, "0")}`;
}

export function moneroWalletView(wallet) {
  const example = wallet.example === true;
  const verified = !example && wallet.state === "ready" && wallet.spending_status === "verified" && wallet.key_images?.complete === true && formatXMR(wallet.balance_atomic) !== "—";
  const pending = wallet.state === "pending";
  const hasIncoming = formatXMR(wallet.received_atomic) !== "—";
  const displayAmount = atomic => formatXMR(atomic).replace(/\.?0+$/, "");
  const balance = verified ? `${displayAmount(wallet.balance_atomic)} XMR` : pending ? "Pending scan…" : hasIncoming ? `${displayAmount(wallet.received_atomic)} XMR received` : "—";
  const notes = [];
  if (pending) {
    const scanned = Number(wallet.scan_height), target = Number(wallet.target_height);
    notes.push(target > 0 ? `Scanning · ${Math.min(100, 100 * scanned / target).toFixed(1)}%` : "Scanning wallet outputs");
  }
  if (verified) notes.push("Balance verified with key images");
  else if (example) notes.push("Public example · received funds only · current balance unknown");
  else notes.push("Spending status unknown · import current key images to verify balance");
  if (wallet.spending_status === "stale" || wallet.state === "error") notes.push("Waiting for a fresh scan");
  if (wallet.error) notes.push(String(wallet.error).slice(0, 200));
  if (wallet.key_images?.present) notes.push(`${wallet.key_images.covered_outputs ?? 0}/${wallet.key_images.total_outputs ?? 0} outputs covered`);
  return { label: String(wallet.name || "Monero wallet"), balance, note: notes.join(" · "), verified, example };
}

export function moneroTotal(wallets) {
  const personal = wallets.filter(wallet => wallet.example !== true);
  if (!personal.length) return "—";
  if (personal.some(wallet => !moneroWalletView(wallet).verified)) return "Pending…";
  const total = personal.reduce((sum, wallet) => sum + BigInt(wallet.balance_atomic), 0n);
  return `${formatXMR(String(total)).replace(/\.?0+$/, "")} XMR`;
}

export function moneroHistoryValues(history) {
  const values = history.filter(item => typeof item.amount_atomic === "string" && /^\d+$/.test(item.amount_atomic)).map(item => BigInt(item.amount_atomic));
  if (!values.length) return [];
  const low = values.reduce((a, b) => a < b ? a : b);
  const high = values.reduce((a, b) => a > b ? a : b);
  const span = high - low || 1n;
  return values.map(value => Number((value - low) * 1000000n / span) / 1000000);
}

export function walletImport(value) {
  if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some(key => !["name", "address", "view_key", "restore_height"].includes(key))) {
    throw new Error("Use a view-only JSON file with address, view_key, and optional name and restore_height. Spending keys and seeds are not accepted.");
  }
  if (value.name !== undefined && typeof value.name !== "string") throw new Error("Wallet name must be text.");
  if (typeof value.address !== "string" || typeof value.view_key !== "string" || !/^[0-9a-fA-F]{64}$/.test(value.view_key)) throw new Error("A primary address and 64-character private view key are required.");
  const height = value.restore_height ?? 0;
  if (!Number.isSafeInteger(height) || height < 0) throw new Error("Restore height must be a non-negative whole number.");
  return { name: value.name?.trim() || "Monero wallet", address: value.address, view_key: value.view_key, restore_height: height };
}

export function keyImagesImport(value) {
  const source = value?.result || value;
  if (!source || !Array.isArray(source.signed_key_images) || source.signed_key_images.length > 5000 || source.signed_key_images.length === 0) throw new Error("Choose a signed key-image JSON export.");
  const offset = source.offset ?? 0;
  if (!Number.isSafeInteger(offset) || offset < 0) throw new Error("Invalid key-image offset.");
  const signed = source.signed_key_images.map(item => {
    if (!/^[0-9a-fA-F]{64}$/.test(item?.key_image || "") || !/^[0-9a-fA-F]{128}$/.test(item?.signature || "")) throw new Error("Invalid signed key-image export.");
    return { key_image: item.key_image, signature: item.signature };
  });
  return { offset, signed_key_images: signed };
}

if (typeof document !== "undefined") {
  const card = document.getElementById("monero-watch");
  const stats = document.getElementById("monero-tower-stats");
  if (card && stats) {
    const el = (tag, cls, text) => {
      const node = document.createElement(tag);
      if (cls) node.className = cls;
      if (text !== undefined) node.textContent = text;
      return node;
    };
    const heading = el("h2", null, "View-only wallets");
    const list = el("div", "watch-list");
    const form = el("form", "watch-add");
    const message = el("p", "watch-msg");
    message.setAttribute("role", "status");
    const input = (label, type = "text", maxLength = 100) => {
      const node = el("input");
      node.type = type;
      node.placeholder = label;
      node.setAttribute("aria-label", label);
      node.autocomplete = "off";
      node.maxLength = maxLength;
      return node;
    };
    const name = input("Monero wallet label (optional)", "text", 40);
    const address = input("Primary Monero address", "text", 95);
    const viewKey = input("Private view key (never a spend key)", "password", 64);
    const height = input("Restore height (0 scans from the beginning)", "number");
    height.min = "0";
    height.step = "1";
    address.required = viewKey.required = true;
    const scanOptions = el("details");
    scanOptions.append(el("summary", null, "Scan options (optional)"), height);
    const fileLabel = el("label", "watch-hint", "Or upload a view-only JSON file");
    const file = input("View-only wallet JSON", "file");
    file.accept = ".json,application/json";
    fileLabel.append(file);
    const add = el("button", null, "Add");
    add.type = "submit";
    add.disabled = true;
    for (const field of [name, address, viewKey, height, file]) field.disabled = true;
    form.append(name, address, viewKey, scanOptions, fileLabel, add);
    const hint = el("p", "watch-hint", "View keys reveal incoming funds and cannot spend. A restore height after your first payment misses earlier funds. Import signed key images to verify spending and current balance; update them after new payments or spending. Never upload a seed or private spend key.");
    card.append(heading, list, form, hint, message);
    let allowed = false;
    let last = { wallets: [] };
    let busy = false;
    const setMessage = (text, error = false) => {
      message.textContent = text;
      message.classList.toggle("watch-msg-error", error);
    };
    async function request(path = "", method = "GET", body) {
      const response = await fetch(API + path, { method, headers: HEADERS, ...(body ? { body: JSON.stringify(body) } : {}) });
      if (!response.ok) throw new Error((await response.text()).slice(0, 200) || "Wallet service unavailable.");
      return response.json();
    }
    function drawHistory(canvas, history) {
      const values = moneroHistoryValues(history);
      const ctx = canvas.getContext("2d");
      const dpr = Math.min(window.devicePixelRatio || 1, 2), width = window.innerWidth >= 900 ? 100 : 48;
      canvas.width = width * dpr;
      canvas.height = 18 * dpr;
      canvas.style.width = `${width}px`;
      canvas.style.height = "18px";
      const pts = points(values, width, 18);
      if (!ctx || !pts.length) return;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.beginPath();
      ctx.moveTo(pts[0].x, pts[0].y);
      for (const point of pts.slice(1)) ctx.lineTo(point.x, point.y);
      ctx.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue("--monero").trim() || "#ff6600";
      ctx.lineWidth = 1.5;
      ctx.stroke();
    }
    function render(data) {
      last = data;
      allowed = data.has_password === true && data.available !== false;
      add.disabled = !allowed || busy;
      for (const field of [name, address, viewKey, height, file]) field.disabled = !allowed || busy;
      list.textContent = stats.textContent = "";
      stats.hidden = !data.wallets?.length;
      for (const wallet of data.wallets || []) {
        const view = moneroWalletView(wallet);
        const row = el("div", "ws-row");
        row.append(el("span", "ws-name", view.label));
        const history = (wallet.history || []).filter(item => item.verified === view.verified);
        if (history.length >= 2) {
          const canvas = el("canvas", "ws-spark");
          canvas.title = view.verified ? "Saved verified balance snapshots" : "Saved incoming-funds snapshots; spending unknown";
          row.append(canvas);
          drawHistory(canvas, history);
        }
        row.append(el("span", "ws-bal", view.balance), el("span", "ws-note", view.note));
        stats.append(row);
        const item = el("div", "monero-wallet-entry");
        item.append(el("strong", null, view.label), el("p", "wk", `${String(wallet.address || "").slice(0, 8)}…${String(wallet.address || "").slice(-8)}`), el("p", "watch-hint", view.note));
        if (view.example && wallet.source_url) {
          const source = el("a", "watch-hint", "Official Monero contribution address");
          source.href = wallet.source_url;
          source.target = "_blank";
          source.rel = "noopener noreferrer";
          item.append(source);
        }
        const keyLabel = el("label", "watch-hint", "Import signed key images (JSON)");
        const images = input(`Key images for ${view.label}`, "file");
        images.accept = ".json,application/json";
        images.disabled = !allowed;
        images.addEventListener("change", async () => {
          try {
            const selected = images.files[0];
            if (!selected) return;
            if (selected.size > 4 * 1024 * 1024) throw new Error("Key-image file is too large (maximum 4 MiB).");
            const payload = keyImagesImport(JSON.parse(await selected.text()));
            await request(`/${encodeURIComponent(wallet.id)}/key-images`, "POST", payload);
            setMessage("Key images imported. Updating spending status…");
          } catch (error) { setMessage(error instanceof SyntaxError ? "Choose a valid JSON file." : error.message, true); }
          finally { images.value = ""; refresh(); }
        });
        keyLabel.append(images);
        const remove = el("button", "watch-x", "Remove");
        remove.type = "button";
        remove.disabled = !allowed;
        remove.setAttribute("aria-label", `Remove ${view.label}`);
        remove.addEventListener("click", async () => {
          try { await request(`/${encodeURIComponent(wallet.id)}`, "DELETE"); setMessage("Wallet removed from monitoring."); }
          catch (error) { setMessage(error instanceof SyntaxError ? "Choose a valid JSON file." : error.message, true); }
          refresh();
        });
        if (!view.example) item.append(keyLabel);
        item.append(remove);
        list.append(item);
      }
      if ((data.wallets || []).filter(wallet => wallet.example !== true).length > 1) {
        const total = el("div", "ws-row ws-total-row");
        total.append(el("span", "ws-name", "Total"), el("span", "ws-bal", moneroTotal(data.wallets)));
        stats.append(total);
      }
      stats.style.left = window.innerWidth >= WIDE_MIN ? `${towerLayout(window.innerWidth).ox}px` : "";
      if (!allowed) setMessage(data.has_password ? "View-only wallet service unavailable. Apply the updated stack to enable it." : "Set a dashboard password to use view-only wallets.");
    }
    async function refresh() {
      try { const data = await request(); card.hidden = false; render(data); }
      catch {
        if (!last.wallets?.length) {
          allowed = false;
          add.disabled = true;
          for (const field of [name, address, viewKey, height, file]) field.disabled = true;
          card.hidden = stats.hidden = true;
          return;
        }
        if (last.wallets?.length) render({ ...last, available: false, wallets: last.wallets.map(wallet => ({ ...wallet, state: "error", spending_status: "stale" })) });
        card.hidden = false;
        setMessage("View-only wallet service unavailable. Check that Monero is enabled and the updated stack is running.", true);
      }
    }
    file.addEventListener("change", async () => {
      try {
        const selected = file.files[0];
        if (!selected) return;
        if (selected.size > 16384) throw new Error("View-only wallet JSON is too large.");
        const value = walletImport(JSON.parse(await selected.text()));
        name.value = value.name || name.value;
        address.value = value.address;
        viewKey.value = value.view_key;
        height.value = value.restore_height;
        setMessage("View-only details loaded. Choose Add to start scanning.");
      } catch (error) { viewKey.value = ""; setMessage(error instanceof SyntaxError ? "Choose a valid JSON file." : error.message, true); }
      finally { file.value = ""; }
    });
    form.addEventListener("submit", async event => {
      event.preventDefault();
      if (!allowed || busy || !form.reportValidity()) return;
      busy = true;
      add.disabled = true;
      try {
        const value = walletImport({ name: name.value, address: address.value.trim(), view_key: viewKey.value.trim(), restore_height: Number(height.value || 0) });
        await request("", "POST", value);
        name.value = address.value = height.value = "";
        setMessage("Added. Scanning for incoming funds…");
      } catch (error) { setMessage(error instanceof SyntaxError ? "Choose a valid JSON file." : error.message, true); }
      finally { viewKey.value = ""; busy = false; refresh(); }
    });
    window.addEventListener("node-selected", event => { if (event.detail?.node !== "monero") viewKey.value = ""; });
    window.addEventListener("resize", () => render(last));
    refresh();
    setInterval(refresh, 15000);
  }
}
