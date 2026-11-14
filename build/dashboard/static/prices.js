import { dashboardFetch as fetch } from "./session.js";

export const PRICE_URL = "/api/prices";
export const FIAT_CURRENCIES = ["USD", "EUR", "GBP", "INR", "CAD", "AUD", "JPY", "RUB", "CNY"];

const POLL_MS = 60000;

export function normalizeCurrency(value) {
  const currency = String(value || "").toUpperCase();
  return FIAT_CURRENCIES.includes(currency) ? currency : "USD";
}

export function priceURL(asset = "bitcoin") {
  return ["monero", "tari", "litecoin", "dogecoin", "bitcoin-cash", "zcash"].includes(asset) ? `${PRICE_URL}?asset=${asset}` : PRICE_URL;
}

export function formatPrice(amount, currency, NumberFormat = Intl.NumberFormat) {
  const value = Number(amount);
  const code = normalizeCurrency(currency);
  try {
    return new NumberFormat(undefined, { style: "currency", currency: code }).format(value);
  } catch {
    return `${code} ${value.toFixed(code === "JPY" ? 0 : 2)}`;
  }
}

export function priceView(data, currency, formatTime = defaultTime) {
  const code = normalizeCurrency(currency);
  const amount = Number(data?.prices?.[code.toLowerCase()]);
  if (data?.available !== true || !Number.isFinite(amount) || amount <= 0) {
    return { currency: code, value: "—", status: "Price unavailable", stale: false };
  }
  const updated = Number(data.updated_at);
  const asOf = Number.isFinite(updated) && updated > 0 ? formatTime(updated) : "unknown";
  return {
    currency: code,
    value: formatPrice(amount, code),
    status: data.stale ? `Stale · as of ${asOf}` : `As of ${asOf}`,
    stale: !!data.stale,
  };
}

export function panelCurrency(openedNode, asset, current, configured) {
  return (openedNode === "bitcoincash" ? "bitcoin-cash" : openedNode) === asset ? normalizeCurrency(configured) : normalizeCurrency(current);
}

export function towerPriceView(data, currency) {
  const view = priceView(data, currency);
  return {
    text: view.value === "—" ? "Price unavailable" : `${view.value}${view.stale ? " · stale" : ""}`,
    status: view.status,
    stale: view.stale,
  };
}

const watched = new Map();
let walletCurrencyCode = "USD";

export function walletCurrency() {
  return walletCurrencyCode;
}

export function watchPrice(asset, listener) {
  let entry = watched.get(asset);
  if (!entry) {
    entry = { data: { available: false }, listeners: new Set() };
    watched.set(asset, entry);
    const poll = async () => {
      try {
        const response = await fetch(priceURL(asset), { headers: { Accept: "application/json" }, credentials: "same-origin" });
        entry.data = response.ok ? await response.json() : { available: false };
      } catch {
        entry.data = { available: false };
      }
      entry.listeners.forEach((notify) => notify());
    };
    poll();
    setInterval(poll, POLL_MS);
  }
  if (listener) entry.listeners.add(listener);
  return entry.data;
}

function defaultTime(unixSeconds) {
  const date = new Date(unixSeconds * 1000);
  try {
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: "medium",
      timeStyle: "short",
    }).format(date);
  } catch {
    return date.toISOString();
  }
}

if (typeof document !== "undefined") {
  let configuredCurrency = "USD";
  const controllers = new Map();
  const assets = new Set([
    ...Array.from(document.querySelectorAll("[data-price-asset]"), element => element.dataset.priceAsset),
    ...Array.from(document.querySelectorAll("[data-tower-price-asset]"), element => element.dataset.towerPriceAsset),
  ]);

  for (const asset of assets) {
    const widget = document.querySelector(`[data-price-asset="${asset}"]`);
    const towerPrices = document.querySelectorAll(`[data-tower-price-asset="${asset}"]`);
    const value = widget?.querySelector("[data-price-value]");
    const currencySelect = widget?.querySelector("[data-price-currency]");
    const status = widget?.querySelector("[data-price-status]");
    if (widget && (!value || !currencySelect || !status)) continue;
    if (!widget && !towerPrices.length) continue;

    if (currencySelect) for (const code of FIAT_CURRENCIES) {
      const option = document.createElement("option");
      option.value = option.textContent = code;
      currencySelect.append(option);
    }

    const controller = { currency: configuredCurrency, latest: { available: false } };
    controllers.set(asset, controller);

    const render = () => {
      if (widget) {
        const view = priceView(controller.latest, controller.currency);
        value.textContent = view.value;
        currencySelect.value = controller.currency;
        status.textContent = view.status;
        status.classList.toggle("stale", view.stale);
        widget.hidden = false;
      }
      const towerView = towerPriceView(controller.latest, configuredCurrency);
      for (const price of towerPrices) {
        price.textContent = towerView.text;
        price.title = towerView.status;
        price.classList.toggle("stale", towerView.stale);
      }
    };

    currencySelect?.addEventListener("change", () => {
      controller.currency = normalizeCurrency(currencySelect.value);
      render();
    });

    const refresh = async () => {
      try {
        const response = await fetch(priceURL(asset), {
          headers: { Accept: "application/json" },
          credentials: "same-origin",
        });
        if (!response.ok) throw new Error("price request failed");
        controller.latest = await response.json();
      } catch {
        controller.latest = { available: false };
      }
      render();
    };

    controller.render = render;
    render();
    refresh();
    setInterval(refresh, POLL_MS);
  }

  document.addEventListener("appearance-updated", event => {
    walletCurrencyCode = normalizeCurrency(event.detail?.default_currency);
    for (const entry of watched.values()) entry.listeners.forEach((notify) => notify());
    configuredCurrency = normalizeCurrency(event.detail?.default_currency);
    for (const controller of controllers.values()) {
      controller.currency = configuredCurrency;
      controller.render();
    }
  });

  window.addEventListener("node-selected", event => {
    const node = event.detail?.node;
    for (const [asset, controller] of controllers) {
      controller.currency = panelCurrency(node, asset, controller.currency, configuredCurrency);
      controller.render();
    }
  });
}
