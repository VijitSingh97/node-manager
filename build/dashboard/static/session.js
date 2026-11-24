export const CSRF_HEADERS = { "X-Requested-With": "fetch" };

export function commandName() {
  return (typeof document !== "undefined" && document.body?.dataset.command) || "./stack";
}

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

export async function jsonRequest(fetcher, path, { method = "GET", payload, failure, definitive = false, redact = false }) {
  const response = await fetcher(path, {
    method,
    headers: { ...(payload && { "Content-Type": "application/json" }), ...CSRF_HEADERS },
    ...(payload ? { body: JSON.stringify(payload) } : {}),
  });
  if (!response.ok) {
    let text = (await response.text()).trim();
    if (redact) text = text.replace(/[0-9a-f]{64}/gi, "[redacted]");
    const error = new Error(text && text.length <= 200 ? text : failure);
    if (definitive) error.definitive = true;
    throw error;
  }
  return response.json();
}

export async function signOut(fetcher = dashboardFetch, location = globalThis.location) {
  const response = await fetcher("/logout", { method: "POST", headers: CSRF_HEADERS });
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
