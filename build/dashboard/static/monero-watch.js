import { atomicToDecimal, bigSeries, formatAmount, joinNote, scanNote } from "./wallet-format.js";
import { el, managerRow, renderWalletList, safeLink, setMessage, walletRequest } from "./wallet-ui.js";

const EXAMPLE_NOTE = "Public example: the official Monero General Fund address, excluded from your totals. A view key reveals funds received but not spending, so this is the total ever received, not a current balance.";

const xmr = (atomic) => atomicToDecimal(atomic, 12);

export function moneroRow(wallet) {
  const example = wallet.example === true;
  const balance = xmr(wallet.balance_atomic);
  const verified = !example && wallet.state === "ready" && wallet.spending_status === "verified" && wallet.key_images?.complete === true && balance !== null;
  const pending = wallet.state === "pending";
  const decimal = pending ? null : verified ? balance : xmr(wallet.received_atomic);
  const status = [];
  if (pending) {
    const scanned = Number(wallet.scan_height), target = Number(wallet.target_height);
    status.push(target > 0 ? `Scanning · ${Math.min(100, 100 * scanned / target).toFixed(1)}%` : "Scanning wallet outputs");
  } else if (verified) status.push("Balance verified with key images");
  else if (decimal !== null) status.push(example ? "Total received" : "Received funds only · import current key images to verify balance");
  if (wallet.spending_status === "stale" || wallet.state === "error") status.push("Waiting for a fresh scan");
  if (wallet.error) status.push(String(wallet.error).slice(0, 200));
  if (wallet.key_images?.present) status.push(`${wallet.key_images.covered_outputs ?? 0}/${wallet.key_images.total_outputs ?? 0} outputs covered`);
  if (!pending) status.push(scanNote(wallet.updated_at));
  const history = (wallet.history || []).filter((item) => item.verified === verified).map((item) => item.amount_atomic);
  return {
    label: String(wallet.name || "Monero wallet"),
    note: example ? EXAMPLE_NOTE : "",
    value: decimal ?? "",
    amount: decimal === null ? "" : formatAmount(decimal, true),
    unit: "XMR",
    stateText: pending ? "Pending scan…" : "—",
    stale: wallet.spending_status === "stale" || wallet.state === "error",
    fiatAsset: "monero",
    status: joinNote(status),
    series: bigSeries(history),
    seriesTitle: verified ? "Saved verified balance snapshots" : "Saved incoming-funds snapshots; spending unknown",
    verified,
    example,
  };
}

export function moneroTotal(wallets) {
  const personal = wallets.filter((wallet) => wallet.example !== true);
  if (!personal.length) return null;
  if (personal.some((wallet) => !moneroRow(wallet).verified)) return { text: "Pending…" };
  const decimal = xmr(String(personal.reduce((sum, wallet) => sum + BigInt(wallet.balance_atomic), 0n)));
  return { text: `${formatAmount(decimal, true)} XMR`, value: decimal, fiatAsset: "monero" };
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
    const request = walletRequest("/api/monero/watch");
    const heading = el("h2", null, "Watch-only wallets");
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
    const say = (text, error = false) => setMessage(message, text, error);
    const errorText = (error) => error instanceof SyntaxError ? "Choose a valid JSON file." : error.message;

    function keyImagesInput(wallet, label) {
      const keyLabel = el("label", "watch-hint", "Import signed key images (JSON)");
      const images = input(`Key images for ${label}`, "file");
      images.accept = ".json,application/json";
      images.disabled = !allowed;
      images.addEventListener("change", async () => {
        try {
          const selected = images.files[0];
          if (!selected) return;
          if (selected.size > 4 * 1024 * 1024) throw new Error("Key-image file is too large (maximum 4 MiB).");
          const payload = keyImagesImport(JSON.parse(await selected.text()));
          await request(`/${encodeURIComponent(wallet.id)}/key-images`, "POST", payload);
          say("Key images imported. Updating spending status…");
        } catch (error) { say(errorText(error), true); }
        finally { images.value = ""; refresh(); }
      });
      keyLabel.append(images);
      return keyLabel;
    }

    function render(data) {
      last = data;
      allowed = data.has_password === true && data.available !== false;
      add.disabled = !allowed || busy;
      for (const field of [name, address, viewKey, height, file]) field.disabled = !allowed || busy;
      const wallets = data.wallets || [];
      const rows = wallets.map(moneroRow);
      const personal = wallets.filter((wallet) => wallet.example !== true);
      renderWalletList(stats, rows, { total: personal.length > 1 ? moneroTotal(wallets) : null });
      list.textContent = "";
      wallets.forEach((wallet, index) => {
        const row = rows[index];
        list.append(managerRow({
          label: row.label,
          note: row.note,
          keyText: String(wallet.address || ""),
          canRemove: allowed,
          onRemove: async () => {
            try { await request(`/${encodeURIComponent(wallet.id)}`, "DELETE"); say("Wallet removed from monitoring."); }
            catch (error) { say(error.message, true); }
            refresh();
          },
          extras: row.example ? [safeLink(wallet.source_url, "Official Monero contribution address")] : [keyImagesInput(wallet, row.label)],
        }));
      });
      if (!allowed) say(data.has_password ? "View-only wallet service unavailable. Apply the updated stack to enable it." : "Set a dashboard password to use view-only wallets.");
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
        render({ ...last, available: false, wallets: last.wallets.map(wallet => ({ ...wallet, state: "error", spending_status: "stale" })) });
        card.hidden = false;
        say("View-only wallet service unavailable. Check that Monero is enabled and the updated stack is running.", true);
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
        say("View-only details loaded. Choose Add to start scanning.");
      } catch (error) { viewKey.value = ""; say(errorText(error), true); }
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
        say("Added. Scanning for incoming funds…");
      } catch (error) { say(errorText(error), true); }
      finally { viewKey.value = ""; busy = false; refresh(); }
    });
    window.addEventListener("node-selected", event => { if (event.detail?.node !== "monero") viewKey.value = ""; });
    refresh();
    setInterval(refresh, 15000);
  }
}
