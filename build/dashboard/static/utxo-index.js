import { dashboardFetch as fetch } from "./session.js";
import { el } from "./wallet-ui.js";

const POLL_MS = 15000;
export const SEED_COMMAND = "./stack utxo-seed";
export const SEED_WARNING = "Seeding takes about 20 minutes, longer if heat pauses it. Bitcoin RPC stops answering for about 5 minutes, then answers slowly until the seed ends. The index takes about 7.5 GiB afterwards and up to about 17 GiB while it runs; 27 GB must be free. Node-down alerts are held for up to 15 minutes. Pause other heavy chains first.";

export const LITECOIN_SEED_COMMAND = "./stack utxo-seed litecoin";
export const LITECOIN_SEED_WARNING = "Litecoin RPC stops answering while litecoind writes the coin set, then answers slowly until the seed ends. Its time and size on mainnet are not measured yet; 27 GB must be free. Litecoin node-down alerts are held for up to 15 minutes, and only one balance index seeds at a time.";

export const BITCOINCASH_SEED_COMMAND = "./stack utxo-seed bitcoincash";
export const BITCOINCASH_SEED_WARNING = "Bitcoin Cash has no coin-set export, so the index replays every block from genesis on an archival node: a long one-time job whose mainnet duration is not measured yet. Bitcoin Cash RPC keeps answering, node-down alerts are not held, heat pauses it, and a stop keeps the blocks already applied. Only one balance index seeds at a time.";

const CHAINS = {
  bitcoin: { card: "utxo-index", heading: "#watch h2", title: "Bitcoin balance index", command: SEED_COMMAND, warning: SEED_WARNING, api: "/api/utxo-index" },
  litecoin: { card: "utxo-index-litecoin", heading: '[data-family-watch="litecoin"] h2', title: "Litecoin balance index", command: LITECOIN_SEED_COMMAND, warning: LITECOIN_SEED_WARNING, api: "/api/utxo-index?chain=litecoin" },
  bitcoincash: { card: "utxo-index-bitcoincash", heading: '[data-family-watch="bitcoincash"] h2', title: "Bitcoin Cash balance index", command: BITCOINCASH_SEED_COMMAND, warning: BITCOINCASH_SEED_WARNING, api: "/api/utxo-index?chain=bitcoincash" },
};

export const CONFIRMED_ONLY = "Balances are confirmed only; unconfirmed and mempool activity is not included.";

const STATE_TEXT = {
  not_seeded: "Not seeded",
  seeding: "Seeding",
  catching_up: "Catching up",
  ready: "Ready",
  degraded: "Degraded",
  invalid: "Needs a new seed",
};

export const OFF_HINT = "Instant balances: switch it on under Settings, Balance index. See ";
export const OFF_HINT_DOCS = "https://github.com/VijitSingh97/node-manager/blob/main/docs/watch-only.md";

export function offHint(status) {
  return status && status.state === "off" && status.node_pruned === true ? OFF_HINT : "";
}

const BADGE = {
  ready: ["ready", "ok"],
  catching_up: ["catching up", "info"],
  seeding: ["seeding", "info"],
  not_seeded: ["needs a seed", "warn"],
  invalid: ["needs a seed", "warn"],
  degraded: ["degraded", "bad"],
};

export function badgeClickTarget(cardHidden) {
  return cardHidden ? "settings" : "anchor";
}

export function indexBadge(status) {
  if (!status) return null;
  if (!status.enabled || status.state === "off") return { text: "Balance index: off", tone: "off" };
  const [label, tone] = BADGE[status.state] || [String(status.state), "warn"];
  return { text: `Balance index: ${label}`, tone };
}

export function indexView(status, chain = "bitcoin") {
  if (!status || !status.enabled || status.state === "off") return null;
  const seeding = status.state === "seeding";
  const idle = status.state === "not_seeded" || status.state === "invalid";
  const rows = [`Index: ${STATE_TEXT[status.state] || status.state}${status.reason ? ` (${status.reason})` : ""}`];
  rows.push(CONFIRMED_ONLY);
  if (seeding) {
    const pct = status.seed_progress == null ? "" : ` ${Math.floor(status.seed_progress * 100)}%`;
    rows.push(`Seed: ${status.phase || "waiting"}${pct}`);
  }
  if (status.alerts_suppressed_until) {
    rows.push(`Node-down alerts are held until ${new Date(status.alerts_suppressed_until).toLocaleTimeString()}.`);
  }
  const last = status.last_seed;
  if (!seeding && last && last.outcome !== "ok") {
    rows.push(`Last seed ${last.outcome}${last.reason ? `: ${last.reason}` : ""}.`);
  }
  const seedNow = idle && status.seed_now === true;
  return {
    rows,
    progress: seeding && status.seed_progress != null ? status.seed_progress : null,
    refusals: (status.refusals || []).map(r => `${r.guard}: ${r.detail}`),
    seedNow,
    command: idle && !seedNow ? CHAINS[chain].command : "",
    warning: idle || seeding ? CHAINS[chain].warning : "",
  };
}

export async function requestSeed(post = fetch, node = "bitcoin") {
  try {
    const res = await post("/api/settings/host", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Requested-With": "fetch" },
      body: JSON.stringify({ action: "utxo_seed", node }),
    });
    if (res.ok) return "Seed requested; the host starts it within a minute.";
    const message = (await res.text()).trim();
    return message && message.length <= 200 ? message : "Could not request a seed.";
  } catch {
    return "Could not request a seed.";
  }
}

function wire(chain) {
  const { card: cardID, heading: headingSelector, title, api } = CHAINS[chain];
  const card = document.getElementById(cardID);
  const heading = document.querySelector(headingSelector);
  let renderBadge = () => {};
  if (card && heading) {
    const badge = el("a", "index-badge");
    badge.href = `#${cardID}`;
    badge.hidden = true;
    badge.addEventListener("click", (event) => {
      if (badgeClickTarget(card.hidden) === "anchor") return;
      event.preventDefault();
      document.getElementById("settings-open")?.click();
    });
    heading.append(badge);
    renderBadge = (status) => {
      const view = indexBadge(status);
      badge.hidden = !view;
      if (!view) return;
      badge.textContent = view.text;
      badge.className = `index-badge index-badge-${view.tone}`;
    };
  }
  if (!card) return;
  const render = (status) => {
    renderBadge(status);
    const view = indexView(status, chain);
    const hint = chain === "bitcoin" ? offHint(status) : "";
    card.hidden = !view && !hint;
    card.textContent = "";
    if (!view) {
      if (hint) {
        const line = el("div", "watch-hint", hint);
        const link = el("a", null, "the docs");
        link.href = OFF_HINT_DOCS;
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        line.append(link, ".");
        card.append(line);
      }
      return;
    }
    card.append(el("h2", null, title));
    for (const row of view.rows) card.append(el("div", "watch-msg", row));
    if (view.progress != null) {
      const bar = el("progress");
      bar.max = 1;
      bar.value = view.progress;
      card.append(bar);
    }
    for (const refusal of view.refusals) card.append(el("div", "watch-warn", refusal));
    if (view.seedNow) {
      const button = el("button", null, "Seed now");
      button.type = "button";
      const note = el("div", "watch-hint", "");
      button.addEventListener("click", async () => {
        button.disabled = true;
        note.textContent = await requestSeed(undefined, chain);
        button.disabled = false;
      });
      card.append(button, note);
    }
    if (view.command) card.append(el("div", "watch-hint", "Start a seed on the host:"), el("code", null, view.command));
    if (view.warning) card.append(el("div", "watch-hint", view.warning));
  };
  const refresh = async () => {
    try {
      const res = await fetch(api, { headers: { "X-Requested-With": "fetch" } });
      if (res.ok) render(await res.json());
    } catch { /* next poll */ }
  };
  refresh();
  setInterval(refresh, POLL_MS);
}

if (typeof document !== "undefined") {
  for (const chain of Object.keys(CHAINS)) wire(chain);
}
