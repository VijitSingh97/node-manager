import { dashboardFetch as fetch, jsonRequest } from "./session.js";

const paths = { health: "/actuator/health", info: "/actuator/info" };
const unsafe = /[\u0000-\u001f\u007f-\u009f]/;

export function shellQuote(value) {
  const text = String(value);
  if (unsafe.test(text)) throw new Error("Control characters cannot be copied safely.");
  return `'${text.replaceAll("'", "'\\''")}'`;
}

export function curlCommand(endpoint, origin, user, password) {
  if (!Object.hasOwn(paths, endpoint)) throw new Error("Unknown endpoint.");
  return `curl --user ${shellQuote(`${user}:${password}`)} ${shellQuote(origin + paths[endpoint])}`;
}

export async function copyText(text, { clipboard = globalThis.navigator?.clipboard, exec } = {}) {
  if (clipboard?.writeText) {
    try { await clipboard.writeText(text); return true; } catch {}
  }
  try { return exec ? exec(text) === true : false; } catch { return false; }
}

function execCopy(host) {
  return (text) => {
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.setAttribute("aria-hidden", "true");
    area.style.cssText = "position:fixed;top:-1000px;left:-1000px;opacity:0";
    host.append(area);
    try {
      area.select();
      return document.execCommand("copy");
    } finally {
      area.remove();
    }
  };
}

export function actuatorCurlController(root = document) {
  const byId = (id) => root.getElementById(id);
  const buttons = { health: byId("settings-actuator-copy-health"), info: byId("settings-actuator-copy-info") };
  const note = byId("settings-actuator-copy-note");
  const dialog = byId("actuator-curl-dialog");
  const form = byId("actuator-curl-form");
  const password = byId("actuator-curl-password");
  const fallback = byId("actuator-curl-fallback");
  const fallbackLabel = byId("actuator-curl-fallback-label");
  const error = byId("actuator-curl-error");
  const submit = byId("actuator-curl-submit");
  const labels = { health: buttons.health.textContent, info: buttons.info.textContent };
  const timers = {};
  let endpoint = "";
  let generation = 0;
  let pending = "";

  const flash = (name, text) => {
    buttons[name].textContent = text;
    clearTimeout(timers[name]);
    timers[name] = setTimeout(() => { buttons[name].textContent = labels[name]; }, 2500);
  };
  const clear = () => {
    generation += 1;
    pending = "";
    submit.textContent = "Copy";
    password.value = fallback.value = "";
    fallbackLabel.hidden = true;
    error.textContent = "";
    submit.disabled = false;
    submit.hidden = false;
  };

  const closeDialog = () => { dialog.close(); clear(); };
  for (const name of Object.keys(buttons)) {
    buttons[name].addEventListener("click", () => {
      endpoint = name;
      clear();
      dialog.showModal();
      password.focus();
    });
  }
  byId("actuator-curl-cancel").addEventListener("click", closeDialog);
  dialog.addEventListener("close", clear);
  dialog.addEventListener("keydown", (event) => { if (event.key === "Escape") event.stopPropagation(); });
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (pending) {
      const name = endpoint;
      const done = copyText(pending, { exec: execCopy(dialog) });
      done.then((ok) => { if (ok) { flash(name, "Copied"); closeDialog(); } else error.textContent = "Copy failed - select the command and press Ctrl+C / Cmd+C."; });
      return;
    }
    if (!password.value) { error.textContent = "Enter the dashboard password."; return; }
    const current = ++generation;
    const name = endpoint;
    submit.disabled = true;
    error.textContent = "Checking password…";
    try {
      const { username } = await jsonRequest(fetch, "/api/settings/actuator-curl", {
        method: "POST",
        payload: { endpoint: name, password: password.value },
        failure: "Could not check the password.",
      });
      if (current !== generation) return;
      const command = curlCommand(name, location.origin, username, password.value);
      if (await copyText(command, { exec: execCopy(dialog) })) {
        flash(name, "Copied");
        closeDialog();
        return;
      }
      flash(name, "Copy failed - select and copy");
      password.value = "";
      pending = command;
      fallback.value = command;
      fallbackLabel.hidden = false;
      submit.textContent = "Copy again";
      submit.disabled = false;
      error.textContent = "";
      fallback.focus();
      fallback.select();
    } catch (failure) {
      if (current === generation) { error.textContent = failure.message; submit.disabled = false; }
    }
  });

  return {
    render(saved, allowed) {
      const on = { health: allowed && saved.actuator_health === true, info: allowed && saved.actuator_info === true };
      for (const name of Object.keys(buttons)) buttons[name].hidden = !on[name];
      note.hidden = on.health || on.info;
    },
    closeDialog() { if (dialog.open) closeDialog(); },
  };
}
