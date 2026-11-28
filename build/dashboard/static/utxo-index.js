import { commandName, dashboardFetch as fetch, jsonRequest } from "./session.js";
import { el } from "./wallet-ui.js";

const POLL_MS = 15000;
export const seedCommand = (chain) => `${commandName()} utxo-seed${chain === "bitcoin" ? "" : ` ${chain}`}`;

const CHAINS = {
  bitcoin: { card: "utxo-index", heading: "#watch h2", title: "Bitcoin balance index", api: "/api/utxo-index" },
  litecoin: { card: "utxo-index-litecoin", heading: '[data-family-watch="litecoin"] h2', title: "Litecoin balance index", api: "/api/utxo-index?chain=litecoin" },
  bitcoincash: { card: "utxo-index-bitcoincash", heading: '[data-family-watch="bitcoincash"] h2', title: "Bitcoin Cash balance index", api: "/api/utxo-index?chain=bitcoincash" },
  dogecoin: { card: "utxo-index-dogecoin", heading: '[data-family-watch="dogecoin"] h2', title: "Dogecoin balance index", api: "/api/utxo-index?chain=dogecoin" },
};

export const CONFIRMED_ONLY = "Balances are confirmed only; unconfirmed and mempool activity is not included.";

export const STATE_LABEL = {
  not_seeded: "not seeded",
  seeding: "seeding",
  catching_up: "catching up",
  ready: "ready",
  degraded: "degraded",
  invalid: "needs a new seed",
};

const GUARD_TEXT = {
  state: "Index state",
  node: "Node",
  thermal: "Temperature",
  cpu_cap: "CPU cap",
  free_space: "Disk",
  memory: "Memory",
  rpc_idle: "Node commands",
  fifo: "Seed pipe",
  request: "Request",
  lock: "Seed slot",
  scanner: "Scanner",
  host: "Host helper",
};

const humanDetail = (r) => (r.guard === "state" ? r.detail.replace(/^index state is (\w+)$/, (_, id) => STATE_LABEL[id] || id.replace(/_/g, " ")) : r.detail);

export const guardText = (r) => `${GUARD_TEXT[r.guard] || "Blocked"}: ${humanDetail(r)}`;

const cap = (text) => text.charAt(0).toUpperCase() + text.slice(1);

export const OFF_HINT = "Instant balances: switch it on under Settings, Balance index. See ";
export const OFF_HINT_DOCS = "https://github.com/VijitSingh97/node-manager/blob/main/docs/watch-only.md";

export function offHint(status) {
  return status && status.state === "off" && status.node_pruned === true ? OFF_HINT : "";
}

const TONE = { ready: "ok", catching_up: "info", seeding: "info", not_seeded: "warn", invalid: "warn", degraded: "bad" };

export function badgeClickTarget(cardHidden) {
  return cardHidden ? "settings" : "anchor";
}

export function indexBadge(status) {
  if (!status) return null;
  if (!status.enabled || status.state === "off") return { text: "Balance index: off", tone: "off" };
  return { text: `Balance index: ${STATE_LABEL[status.state] || status.state}`, tone: TONE[status.state] || "warn" };
}

export function indexView(status, chain = "bitcoin") {
  if (!status || !status.enabled || status.state === "off") return null;
  const seeding = status.state === "seeding";
  const idle = status.state === "not_seeded" || status.state === "invalid";
  const rows = [`Index: ${cap(STATE_LABEL[status.state] || String(status.state))}${status.reason ? ` (${status.reason})` : ""}`];
  rows.push(CONFIRMED_ONLY);
  if (seeding) {
    const pct = status.seed_progress == null ? "" : ` ${Math.floor(status.seed_progress * 100)}%`;
    rows.push(`Seed: ${status.phase || "waiting"}${pct}`);
  }
  if (status.p2pk_missing) {
    rows.push(`Coins paid directly to a public key (P2PK) are not counted until this index is rebuilt. It rebuilds on its own when the node is synced; to rebuild now run: ${seedCommand(chain)}`);
  }
  if (status.alerts_suppressed_until) {
    rows.push(`Node-down alerts are held until ${new Date(status.alerts_suppressed_until).toLocaleTimeString()}.`);
  }
  const last = status.last_seed;
  if (!seeding && last && last.outcome !== "ok") {
    rows.push(`Last seed ${last.outcome}${last.reason ? `: ${last.reason}` : ""}.`);
  }
  return {
    rows,
    progress: seeding && status.seed_progress != null ? status.seed_progress : null,
    refusals: (status.refusals || []).map(guardText),
    command: idle && status.needs_host === true ? seedCommand(chain) : "",
  };
}

const shortReason = (text) => {
  const line = String(text || "").trim();
  return line.length > 80 ? `${line.slice(0, 79)}…` : line;
};

const WAITING_START = new Set(["node_start", "other_seed", "host_busy"]);

// value is a fraction, or null for an indeterminate bar.
export function indexBarView(status) {
  if (!status || !status.enabled || status.state === "off") return { hidden: true };
  const bar = (text, value, tone = "info") => ({ hidden: false, label: `Balance index: ${text}`, value, tone });
  const pct = status.seed_progress == null ? null : Math.floor(status.seed_progress * 100);
  if (status.seed_stopped) {
    const reason = shortReason(status.last_seed && status.last_seed.reason);
    return bar(`seeding ${status.seed_stopped === "cancelled" ? "stopped" : "failed"}${reason ? `: ${reason}` : ""}`, null, "warn");
  }
  switch (status.state) {
    case "ready":
      return bar("ready", 1, "ok");
    case "seeding": {
      const paused = status.phase === "paused";
      return bar(`${paused ? "paused" : "building"}${pct == null ? "" : `, ${pct}%`}`, status.seed_progress);
    }
    case "catching_up":
      if (!status.blocks_behind && status.reason === "waiting for the node to sync") return bar(status.reason, null);
      return bar(status.blocks_behind == null ? "catching up" : `catching up ${status.blocks_behind} blocks`, null);
    case "degraded":
    case "invalid": {
      const reason = shortReason(status.reason);
      const held = status.waiting_for ? shortReason(status.refusals && status.refusals[0] && status.refusals[0].detail) : "";
      return bar(`${STATE_LABEL[status.state]}${reason ? `: ${reason}` : ""}${held ? `, reseed waiting: ${held}` : ""}`, null, "warn");
    }
    case "not_seeded": {
      if (status.needs_host) return bar(`${STATE_LABEL.not_seeded}, needs the host helper`, null, "warn");
      if (status.waiting_for === "node_sync") return bar("waiting for the node to sync", null);
      if (WAITING_START.has(status.waiting_for)) return bar("waiting to start", null);
      if (status.waiting_for === "blocked") {
        const detail = shortReason(status.refusals && status.refusals[0] && status.refusals[0].detail);
        return bar(`waiting to start${detail ? `: ${detail}` : ""}`, null, "warn");
      }
      return bar("starting", null);
    }
  }
  return bar(String(status.state), null, "warn");
}

export function applyIndexBar(bar, view) {
  if (!bar) return;
  bar.hidden = view.hidden;
  if (view.hidden) return;
  bar.querySelector(".index-bar-label").textContent = view.label.replace(/^Balance index: /, "");
  const track = bar.querySelector(".progress-bg");
  const fill = bar.querySelector(".index-bar-fill");
  bar.className = `index-bar index-bar-${view.value == null ? "indeterminate" : "determinate"} index-bar-${view.tone}`;
  fill.style.width = view.value == null ? "" : `${Math.round(view.value * 100)}%`;
  track.setAttribute("aria-label", view.label);
  if (view.value == null) track.removeAttribute("aria-valuenow");
  else track.setAttribute("aria-valuenow", String(Math.round(view.value * 100)));
}

function wire(chain) {
  const { card: cardID, heading: headingSelector, title, api } = CHAINS[chain];
  let lastStatus;
  const renderBar = () => applyIndexBar(document.getElementById(`index-bar-${chain}`), indexBarView(lastStatus));
  window.addEventListener("live-updated", renderBar);
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
  const render = (status) => {
    lastStatus = status;
    renderBar();
    renderBadge(status);
    if (!card) return;
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
    if (view.command) card.append(el("div", "watch-hint", "Start a seed on the host:"), el("code", null, view.command));
  };
  const refresh = async () => {
    try {
      render(await jsonRequest(fetch, api, { failure: "unavailable" }));
    } catch { /* next poll */ }
  };
  refresh();
  setInterval(refresh, POLL_MS);
}

if (typeof document !== "undefined") {
  for (const chain of Object.keys(CHAINS)) {
    if (document.getElementById(CHAINS[chain].card) || document.getElementById(`index-bar-${chain}`)) wire(chain);
  }
}
