import { dashboardFetch as fetch } from "./session.js";

const nodes = ["bitcoin", "monero", "litecoin", "dogecoin", "bitcoincash", "zcash"];
const labels = {
  bitcoin: "Bitcoin RPC",
  monero: "Monero RPC",
  litecoin: "Litecoin RPC",
  dogecoin: "Dogecoin RPC",
  bitcoincash: "Bitcoin Cash RPC",
  zcash: "Zcash RPC",
};
const states = ["pending", "applying", "applied", "rejected", "failed"];

export function passwordChangeError(oldPassword, newPassword, repeatPassword, passwordConfigured = true) {
  if (passwordConfigured && !oldPassword) return "Enter your current dashboard password.";
  for (const value of [oldPassword, newPassword, repeatPassword]) {
    if (/[\u0000-\u001f\u007f-\u009f]/.test(String(value ?? ""))) return "Passwords cannot contain control characters.";
  }
  if (String(oldPassword ?? "").length > 256) return "The current password is too long.";
  if (String(newPassword ?? "").length < 12) return "The new password must be at least 12 characters.";
  if (String(newPassword ?? "").length > 128) return "The new password must be no more than 128 characters.";
  if (newPassword !== repeatPassword) return "The new passwords do not match.";
  return "";
}

export function credentialControlModel(data) {
  const interfaces = {};
  for (const item of Array.isArray(data?.interfaces) ? data.interfaces : []) {
    if (!nodes.includes(item?.node) || interfaces[item.node]) continue;
    interfaces[item.node] = { node: item.node, enabled: item.enabled === true };
  }
  const source = data?.status && typeof data.status === "object" ? data.status : data;
  const state = states.includes(source?.state) ? source.state : "";
  const node = state && nodes.includes(source?.node) ? source.node : "";
  const message = state
    ? String((typeof data?.status === "string" ? data.status : source?.message) || "")
    : "";
  return { available: data?.available === true, requiresPassword: data?.requires_password === true, interfaces, status: { state, node, message } };
}

export function shouldPollCredentials(dialogOpen, state) {
  return dialogOpen === true && ["pending", "applying"].includes(state);
}

function eyeIcon(hidden) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  svg.classList.add("credential-eye");
  const eye = document.createElementNS(svg.namespaceURI, "path");
  eye.setAttribute("d", "M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12zm10 3a3 3 0 1 0 0-6 3 3 0 0 0 0 6z");
  svg.append(eye);
  if (hidden) {
    const slash = document.createElementNS(svg.namespaceURI, "path");
    slash.setAttribute("d", "M3 3l18 18");
    slash.setAttribute("fill", "none");
    slash.setAttribute("stroke", "currentColor");
    slash.setAttribute("stroke-width", "2");
    svg.append(slash);
  }
  return svg;
}

function revealLabel(button, visible) {
  button.replaceChildren(eyeIcon(!visible), document.createTextNode(visible ? " Hide password" : " Show password"));
  button.setAttribute("aria-label", visible ? "Hide RPC username and password" : "Show RPC username and password");
}

function credentialPanel(row, node) {
  const panel = document.createElement("div");
  panel.className = "settings-credential";
  panel.hidden = true;
  const actions = document.createElement("div");
  actions.className = "settings-credential-actions";
  const reveal = document.createElement("button");
  reveal.type = "button";
  reveal.dataset.credentialReveal = node;
  revealLabel(reveal, false);
  const reset = document.createElement("button");
  reset.type = "button";
  reset.dataset.credentialReset = node;
  reset.textContent = "Reset password";
  actions.append(reveal, reset);
  const secret = document.createElement("dl");
  secret.className = "settings-credential-secret";
  secret.hidden = true;
  const usernameLabel = document.createElement("dt");
  usernameLabel.textContent = "Username";
  const username = document.createElement("dd");
  username.dataset.credentialUsername = "";
  const passwordLabel = document.createElement("dt");
  passwordLabel.textContent = "Password";
  const password = document.createElement("dd");
  password.dataset.credentialPassword = "";
  secret.append(usernameLabel, username, passwordLabel, password);
  const status = document.createElement("p");
  status.className = "settings-note";
  status.dataset.credentialStatus = "";
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  panel.append(actions, secret, status);
  row.append(panel);
  return { panel, reveal, reset, secret, username, password, status };
}

export function credentialSettingsController(settingsDialog, root = document) {
  const byId = (id) => document.getElementById(id);
  const password = {
    old: byId("settings-password-old"),
    next: byId("settings-password-new"),
    repeat: byId("settings-password-repeat"),
    save: byId("settings-password-save"),
    reload: byId("settings-password-reload"),
    status: byId("settings-password-status"),
    oldLabel: byId("settings-password-old-label"),
  };
  const resetDialog = byId("credential-reset-dialog");
  const resetForm = byId("credential-reset-form");
  const resetDescription = byId("credential-reset-description");
  const resetConfirm = byId("credential-reset-confirm");
  const resetSubmit = byId("credential-reset-submit");
  const resetError = byId("credential-reset-error");
  const panels = Object.fromEntries(nodes.map((node) => {
    const row = root.querySelector(`[data-credential-node="${node}"]`);
    return [node, credentialPanel(row, node)];
  }));
  let available = false;
  let credentialState = "";
  let credentialNode = "";
  let generation = 0;
  let passwordGeneration = 0;
  let passwordConfigured = true;
  let poll;
  let resetNode = "";

  const active = (current) => current === generation;
  const passwordActive = (current) => settingsDialog.open && current === passwordGeneration;
  const request = async (path, payload) => {
    const response = await fetch(path, {
      method: payload ? "POST" : "GET",
      headers: payload ? { "Content-Type": "application/json", "X-Requested-With": "fetch" } : {},
      ...(payload ? { body: JSON.stringify(payload) } : {}),
    });
    if (!response.ok) {
      const text = (await response.text()).trim();
      throw new Error(text.length <= 200 ? text : "Could not update credentials.");
    }
    return response.json();
  };

  const hideSecret = (node) => {
    const view = panels[node];
    view.username.textContent = "";
    view.password.textContent = "";
    view.secret.hidden = true;
    revealLabel(view.reveal, false);
  };
  const clearSecrets = () => nodes.forEach(hideSecret);
  const clearPassword = () => {
    password.old.value = password.next.value = password.repeat.value = "";
  };
  const render = (data, current) => {
    if (!active(current)) return;
    const view = credentialControlModel(data);
    available = view.available;
    credentialState = view.status.state;
    credentialNode = view.status.node;
    for (const node of nodes) {
      const enabled = view.interfaces[node]?.enabled === true;
      panels[node].panel.hidden = !enabled;
      panels[node].reveal.disabled = !enabled || view.requiresPassword;
      panels[node].reset.disabled = !enabled || !available || shouldPollCredentials(true, view.status.state);
      panels[node].status.textContent = view.status.node === node
        ? view.status.message
        : enabled && view.requiresPassword ? "Set a dashboard password in global settings to reveal or reset RPC credentials."
          : enabled && !available ? "Password reset requires the installed host helper." : "";
      if (!enabled) hideSecret(node);
    }
    clearTimeout(poll);
    if (shouldPollCredentials(true, view.status.state)) poll = setTimeout(() => load(current), 2000);
  };
  const load = async (current = generation) => {
    try { render(await request("/api/settings/credentials"), current); }
    catch (error) {
      if (!active(current)) return;
      if (shouldPollCredentials(true, credentialState)) {
        if (panels[credentialNode]) panels[credentialNode].status.textContent = "Reconnecting after the password reset…";
        poll = setTimeout(() => load(current), 2000);
      } else {
        available = false;
        for (const view of Object.values(panels)) {
          view.panel.hidden = true;
          view.status.textContent = error.message;
        }
      }
    }
  };

  password.save.addEventListener("click", async () => {
    const error = passwordChangeError(password.old.value, password.next.value, password.repeat.value, passwordConfigured);
    password.status.textContent = error;
    password.reload.hidden = true;
    if (error) return;
    const current = passwordGeneration;
    const payload = { old_password: password.old.value, new_password: password.next.value, repeat_password: password.repeat.value };
    password.save.disabled = true;
    password.status.textContent = "Changing password…";
    try {
      const result = await request("/api/settings/password", payload);
      if (!passwordActive(current)) return;
      if (result?.changed !== true) throw new Error("The password was not changed.");
      password.status.textContent = "Password changed. Sign in again with your new password.";
      password.reload.hidden = false;
    } catch (requestError) {
      if (passwordActive(current)) password.status.textContent = requestError.message;
    } finally {
      clearPassword();
      if (passwordActive(current)) password.save.disabled = false;
    }
  });
  for (const input of [password.old, password.next, password.repeat]) {
    input.addEventListener("keydown", (event) => {
      if (event.key !== "Enter") return;
      event.preventDefault();
      password.save.click();
    });
  }
  password.reload.addEventListener("click", () => location.replace("/login"));

  for (const node of nodes) {
    const view = panels[node];
    view.reveal.addEventListener("click", async () => {
      if (!view.secret.hidden) { hideSecret(node); return; }
      const current = generation;
      view.reveal.disabled = true;
      view.status.textContent = "Loading credentials…";
      try {
        const result = await request("/api/settings/credentials/reveal", { node });
        if (!active(current)) return;
        if (typeof result?.username !== "string" || typeof result?.password !== "string") throw new Error("Credentials are unavailable.");
        view.username.textContent = result.username;
        view.password.textContent = result.password;
        view.secret.hidden = false;
        revealLabel(view.reveal, true);
        view.status.textContent = "";
      } catch (error) {
        if (active(current)) view.status.textContent = error.message;
      } finally {
        if (active(current)) view.reveal.disabled = false;
      }
    });
    view.reset.addEventListener("click", () => {
      resetNode = node;
      resetDescription.textContent = `${labels[node]} will restart. Wallets and other clients will need the new password.`;
      resetConfirm.value = "";
      resetSubmit.disabled = true;
      resetError.textContent = "";
      resetDialog.showModal();
      resetConfirm.focus();
    });
  }

  resetConfirm.addEventListener("input", () => { resetSubmit.disabled = resetConfirm.value !== "reset"; });
  byId("credential-reset-cancel").addEventListener("click", () => resetDialog.close());
  resetDialog.addEventListener("keydown", (event) => { if (event.key === "Escape") event.stopPropagation(); });
  resetDialog.addEventListener("close", () => {
    resetNode = "";
    resetConfirm.value = "";
    resetError.textContent = "";
  });
  resetForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!resetNode || resetConfirm.value !== "reset" || !available) return;
    const node = resetNode;
    const current = generation;
    hideSecret(node);
    resetSubmit.disabled = true;
    resetError.textContent = "Resetting password…";
    try {
      const result = await request("/api/settings/credentials/reset", { node, confirmation: "reset" });
      if (!active(current)) return;
      resetDialog.close();
      const view = credentialControlModel(result);
      credentialState = view.status.state;
      credentialNode = view.status.node || node;
      panels[node].status.textContent = view.status.message || "Password reset queued.";
      clearTimeout(poll);
      if (shouldPollCredentials(true, credentialState)) poll = setTimeout(() => load(current), 500);
      else await load(current);
    } catch (error) {
      if (active(current)) resetError.textContent = error.message;
    } finally {
      resetConfirm.value = "";
      if (active(current) && resetDialog.open) resetSubmit.disabled = true;
    }
  });

  return {
    start() {
      generation += 1;
      credentialState = credentialNode = "";
      clearSecrets();
      load(generation);
    },
    openPassword() {
      passwordGeneration += 1;
      clearPassword();
      password.save.disabled = false;
      password.reload.hidden = true;
      password.status.textContent = "";
    },
    setPasswordConfigured(configured) {
      passwordConfigured = configured === true;
      password.oldLabel.hidden = !passwordConfigured;
      password.save.textContent = passwordConfigured ? "Change password" : "Set password";
    },
    closePassword() {
      passwordGeneration += 1;
      clearPassword();
    },
    closeNode() {
      generation += 1;
      clearTimeout(poll);
      credentialState = credentialNode = "";
      if (resetDialog.open) resetDialog.close();
      clearSecrets();
    },
    openNode() {
      generation += 1;
      credentialState = credentialNode = "";
      clearSecrets();
      load(generation);
    },
  };
}
