function requestURL(input) {
  if (typeof window === "undefined" || typeof document === "undefined") return null;
  const value = typeof input === "string" || input instanceof URL ? input : input?.url;
  if (!value) return null;
  try {
    return new URL(value, document.baseURI || window.location.href);
  } catch {
    return null;
  }
}

export async function dashboardFetch(input, init) {
  const response = await globalThis.fetch(input, init);
  const url = requestURL(input);
  if (response.status === 401 && url && url.origin === window.location.origin && (url.pathname === "/" || url.pathname.startsWith("/api/"))) {
    window.location.replace("/login");
  }
  return response;
}

export async function signOut(fetcher = dashboardFetch, location = globalThis.location) {
  const response = await fetcher("/logout", { method: "POST", headers: { "X-Requested-With": "fetch" } });
  if (response.ok) location.replace("/login");
  return response;
}

if (typeof document !== "undefined") {
  const button = document.getElementById("session-logout");
  button?.addEventListener("click", async () => {
    button.disabled = true;
    try {
      const response = await signOut();
      if (!response.ok) button.disabled = false;
    } catch {
      button.disabled = false;
    }
  });
}
