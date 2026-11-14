import { dashboardFetch as fetch } from "./session.js";
import { el } from "./wallet-ui.js";

const POLL_MS = 15000;
export const SEED_COMMAND = "./stack utxo-seed";
export const SEED_WARNING = "Seeding makes Bitcoin RPC fail for about 5 minutes and run degraded for about 5 more (at least 15 minutes in total). It needs 27 GB free and holds node-down alerts for up to 15 minutes. Pause other heavy chains first.";

const STATE_TEXT = {
  not_seeded: "Not seeded",
  seeding: "Seeding",
  catching_up: "Catching up",
  ready: "Ready",
  degraded: "Degraded",
  invalid: "Needs a new seed",
};

export function indexView(status) {
  if (!status || !status.enabled || status.state === "off") return null;
  const seeding = status.state === "seeding";
  const idle = status.state === "not_seeded" || status.state === "invalid";
  const rows = [`Index: ${STATE_TEXT[status.state] || status.state}${status.reason ? ` (${status.reason})` : ""}`];
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
  return {
    rows,
    progress: seeding && status.seed_progress != null ? status.seed_progress : null,
    refusals: (status.refusals || []).map(r => `${r.guard}: ${r.detail}`),
    command: idle ? SEED_COMMAND : "",
    warning: idle || seeding ? SEED_WARNING : "",
  };
}

if (typeof document !== "undefined") {
  const card = document.getElementById("utxo-index");
  if (card) {
    const render = (status) => {
      const view = indexView(status);
      card.hidden = !view;
      if (!view) return;
      card.textContent = "";
      card.append(el("h2", null, "Bitcoin balance index"));
      for (const row of view.rows) card.append(el("div", "watch-msg", row));
      if (view.progress != null) {
        const bar = el("progress");
        bar.max = 1;
        bar.value = view.progress;
        card.append(bar);
      }
      for (const refusal of view.refusals) card.append(el("div", "watch-warn", refusal));
      if (view.command) card.append(el("div", "watch-hint", "Start a seed on the host:"), el("code", null, view.command));
      if (view.warning) card.append(el("div", "watch-hint", view.warning));
    };
    const refresh = async () => {
      try {
        const res = await fetch("/api/utxo-index", { headers: { "X-Requested-With": "fetch" } });
        if (res.ok) render(await res.json());
      } catch { /* next poll */ }
    };
    refresh();
    setInterval(refresh, POLL_MS);
  }
}
