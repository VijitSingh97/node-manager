// Keep persistent controls mounted while the node's status changes.
import { dashboardFetch as fetch } from "./session.js";

const INTERVAL_MS = 30000;

function layout(doc) {
  return `${!!doc.getElementById("watch")}:${!!doc.getElementById("btc-price")}`;
}

export function needsFullReload(current, next) {
  return layout(current) !== layout(next);
}

export function replaceUpdateViews(current, next) {
  for (const id of ["update-notice", "settings-update"]) {
    const currentView = current.getElementById(id);
    const nextView = next.getElementById(id);
    if (currentView && nextView) currentView.replaceWith(nextView);
  }
}

export function replaceLive(current, next) {
  const nextLive = next.getElementById("live");
  const currentLive = current.getElementById("live");
  if (!nextLive || !currentLive) return false;
  const price = current.getElementById("btc-price");
  const nextPrice = nextLive.querySelector("#btc-price");
  if (price && nextPrice) nextPrice.replaceWith(price);
  replaceUpdateViews(current, next);
  currentLive.replaceWith(nextLive);
  for (const name of ["center", "center-screen"]) {
    current.body.classList.toggle(name, next.body.classList.contains(name));
  }
  return true;
}

if (typeof document !== "undefined") {
  let reloading = false;

  async function tick() {
    try {
      const res = await fetch(location.pathname, { headers: { "X-Requested-With": "fetch" } });
      if (!res.ok) return;
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      if (needsFullReload(document, doc)) {
        if (!reloading) {
          reloading = true;
          window.location.reload();
        }
        return;
      }
      if (replaceLive(document, doc)) {
        window.dispatchEvent(new Event("live-updated"));
      }
    } catch {
      /* offline or a blip — try again next tick */
    }
  }

  window.addEventListener("node-power-updated", event => {
    if (event.detail?.id === "bitcoin" && event.detail?.active) tick();
  });
  setInterval(tick, INTERVAL_MS);
}
