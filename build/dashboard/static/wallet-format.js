import { formatPrice, normalizeCurrency } from "./prices.js";

export function groupDigits(whole) {
  return String(whole).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

export function formatAmount(decimal, trim = false) {
  let text = String(decimal ?? "");
  if (!/^\d+(\.\d+)?$/.test(text)) return "";
  if (trim && text.includes(".")) text = text.replace(/0+$/, "").replace(/\.$/, "");
  const [whole, fraction] = text.split(".");
  return fraction ? `${groupDigits(whole)}.${fraction}` : groupDigits(whole);
}

export function atomicToDecimal(atomic, decimals) {
  if (typeof atomic !== "string" || !/^\d+$/.test(atomic)) return null;
  const scale = 10n ** BigInt(decimals);
  const value = BigInt(atomic);
  return `${value / scale}.${String(value % scale).padStart(decimals, "0")}`;
}

export function fiatLabel(amount, price, currency) {
  const value = typeof amount === "number" ? amount : parseFloat(amount);
  const rate = Number(price?.prices?.[normalizeCurrency(currency).toLowerCase()]);
  if (price?.available !== true || !Number.isFinite(value) || !Number.isFinite(rate) || rate <= 0) return "";
  return `≈ ${formatPrice(value * rate, currency)}${price.stale ? " · stale" : ""}`;
}

export function scanNote(time) {
  if (!time) return "";
  const date = typeof time === "number" ? new Date(time * 1000) : new Date(time);
  return Number.isFinite(date.getTime()) ? `Scanned ${date.toLocaleString()}` : "";
}

export function joinNote(parts) {
  return parts.filter(Boolean).join(" · ");
}

export function bigSeries(values) {
  const amounts = values.filter((value) => typeof value === "string" && /^\d+$/.test(value) && value.length <= 40).map(BigInt);
  if (amounts.length < 2) return [];
  const low = amounts.reduce((a, b) => a < b ? a : b);
  const high = amounts.reduce((a, b) => a > b ? a : b);
  const span = high - low || 1n;
  return amounts.map((value) => Number((value - low) * 1000000n / span) / 1000000);
}

export function shortKey(key) {
  return key.length > 12 ? `${key.slice(0, 4)}…${key.slice(-4)}` : key;
}

export function httpsURL(value) {
  try {
    const url = new URL(String(value || ""));
    return url.protocol === "https:" ? url.href : "";
  } catch {
    return "";
  }
}
