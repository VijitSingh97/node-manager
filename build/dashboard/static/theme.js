// Live theme toggle (Auto -> Light -> Dark). The inline theme-init script in
// <head> sets the initial data-theme before first paint to avoid a flash;
// this module owns the toggle button and keeps localStorage in sync.
//
// An ES module so the pure cycle logic is importable by theme.test.mjs. The
// DOM wiring is guarded on `document` so importing in Node touches nothing.
export const STORAGE_KEY = "dashboardTheme";
export const MODES = ["auto", "light", "dark"];

export function nextTheme(current) {
  const i = MODES.indexOf(current);
  return MODES[(i + 1) % MODES.length]; // unknown -> index -1 -> "auto"
}

export function label(mode) {
  return { auto: "Auto", light: "Light", dark: "Dark" }[mode] || "Auto";
}

if (typeof document !== "undefined") {
  const read = () => {
    try {
      const t = localStorage.getItem(STORAGE_KEY);
      return MODES.includes(t) ? t : "auto";
    } catch {
      return "auto";
    }
  };

  const apply = (mode) => {
    document.documentElement.setAttribute("data-theme", mode);
    document.querySelectorAll(".theme-toggle").forEach((btn) => {
      btn.textContent = label(mode);
      btn.setAttribute("aria-label", `Switch theme, now ${label(mode)}`);
    });
  };

  const wire = () => {
    apply(read());
    document.querySelectorAll(".theme-toggle").forEach((btn) => btn.addEventListener("click", () => {
      const mode = nextTheme(read());
      try {
        localStorage.setItem(STORAGE_KEY, mode);
      } catch {
        /* storage blocked; the choice just won't persist */
      }
      apply(mode);
    }));
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", wire);
  } else {
    wire();
  }
}
