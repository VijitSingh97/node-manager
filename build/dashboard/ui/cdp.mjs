import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const MAC_CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

export function findChrome(env = process.env) {
  const dirs = (env.PATH || "").split(":");
  for (const candidate of [env.CHROME, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", MAC_CHROME]) {
    if (!candidate) continue;
    const found = candidate.includes("/") ? [candidate] : dirs.map((dir) => join(dir, candidate));
    const path = found.find((file) => existsSync(file));
    if (path) return path;
  }
  return "";
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const KEYS = {
  Enter: { key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, text: "\r" },
  Escape: { key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 },
  Backspace: { key: "Backspace", code: "Backspace", windowsVirtualKeyCode: 8 },
};

class Connection {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    this.listeners = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);
      if (message.id) {
        const { resolve, reject } = this.pending.get(message.id) || {};
        this.pending.delete(message.id);
        if (message.error) reject?.(new Error(`${message.error.message}`));
        else resolve?.(message.result);
        return;
      }
      for (const listener of this.listeners.get(message.sessionId || "") || []) listener(message.method, message.params);
    });
  }

  send(method, params = {}, sessionId) {
    const id = ++this.nextID;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`Chrome did not answer ${method}`)), 60000);
      this.pending.set(id, { resolve: (value) => { clearTimeout(timer); resolve(value); }, reject: (error) => { clearTimeout(timer); reject(error); } });
      this.socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    });
  }

  on(sessionId, listener) {
    if (!this.listeners.has(sessionId)) this.listeners.set(sessionId, []);
    this.listeners.get(sessionId).push(listener);
  }
}

export class Page {
  constructor(connection, sessionId, targetId, contextId) {
    this.connection = connection;
    this.sessionId = sessionId;
    this.targetId = targetId;
    this.contextId = contextId;
    this.errors = [];
    this.dialogs = [];
    this.requests = [];
    this.confirmAnswer = undefined;
    connection.on(sessionId, (method, params) => this.#event(method, params));
  }

  send(method, params) {
    return this.connection.send(method, params, this.sessionId);
  }

  #event(method, params) {
    if (method === "Runtime.exceptionThrown") this.errors.push(`uncaught: ${params.exceptionDetails.exception?.description || params.exceptionDetails.text}`);
    else if (method === "Runtime.consoleAPICalled" && params.type === "error") this.errors.push(`console.error: ${params.args.map((arg) => arg.value ?? arg.description).join(" ")}`);
    else if (method === "Network.requestWillBeSent" && params.request.url.startsWith(this.origin || "http")) {
      const { method: verb, url, postData, headers } = params.request;
      const body = postData && String(headers["Content-Type"] || "").includes("json") ? JSON.parse(postData) : undefined;
      const target = new URL(url);
      if (!target.pathname.startsWith("/static/")) this.requests.push({ method: verb, url: target.pathname + target.search, body });
    } else if (method === "Page.javascriptDialogOpening") {
      this.dialogs.push({ type: params.type, message: params.message });
      const accept = params.type === "confirm" ? this.confirmAnswer : undefined;
      if (accept === undefined) this.errors.push(`unexpected ${params.type} dialog: ${params.message}`);
      this.send("Page.handleJavaScriptDialog", { accept: accept === true }).catch(() => {});
    }
  }

  async init() {
    await Promise.all(["Page.enable", "Runtime.enable", "Network.enable", "DOM.enable"].map((method) => this.send(method)));
    await this.send("Emulation.setDeviceMetricsOverride", { width: 1280, height: 1000, deviceScaleFactor: 1, mobile: false });
  }

  async goto(url) {
    this.origin = new URL(url).origin;
    await this.evaluate(() => { window.navigationMarker = true; });
    await this.send("Page.navigate", { url });
    await this.waitFor(() => !window.navigationMarker && document.readyState === "complete", { description: "page load" });
  }

  async evaluate(fn, ...args) {
    const expression = typeof fn === "function" ? `(${fn})(...${JSON.stringify(args)})` : fn;
    const { result, exceptionDetails } = await this.send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
    if (exceptionDetails) throw new Error(exceptionDetails.exception?.description || exceptionDetails.text);
    return result.value;
  }

  async waitFor(predicate, { timeout = 20000, description = String(predicate).slice(0, 80), args = [] } = {}) {
    const deadline = Date.now() + timeout;
    let last;
    while (Date.now() < deadline) {
      try {
        last = await this.evaluate(predicate, ...args);
        if (last) return last;
      } catch (error) {
        last = error;
      }
      await sleep(20);
    }
    throw new Error(`timed out waiting for ${description}${last instanceof Error ? `: ${last.message}` : ""}`);
  }

  requestsTo(method, path) {
    return this.requests.filter((request) => request.method === method && request.url === path);
  }

  visible(selector) {
    return this.evaluate((sel) => {
      const element = document.querySelector(sel);
      if (!element) return false;
      const style = getComputedStyle(element);
      return style.display !== "none" && style.visibility !== "hidden" && element.getClientRects().length > 0 && !element.closest("[hidden]");
    }, selector);
  }

  text(selector) {
    return this.evaluate((sel) => document.querySelector(sel)?.textContent.trim() ?? null, selector);
  }

  value(selector) {
    return this.evaluate((sel) => document.querySelector(sel)?.value ?? null, selector);
  }

  checked(selector) {
    return this.evaluate((sel) => document.querySelector(sel)?.checked ?? null, selector);
  }

  disabled(selector) {
    return this.evaluate((sel) => document.querySelector(sel)?.disabled ?? null, selector);
  }

  async waitVisible(selector, options) {
    await this.waitFor((sel) => {
      const element = document.querySelector(sel);
      return !!element && getComputedStyle(element).display !== "none" && element.getClientRects().length > 0 && !element.closest("[hidden]");
    }, { description: `${selector} to be visible`, args: [selector], ...options });
  }

  async waitText(selector, expected, options) {
    await this.waitFor((sel, want) => {
      const text = document.querySelector(sel)?.textContent.trim() ?? "";
      return want instanceof Object ? new RegExp(want.pattern).test(text) : text === want;
    }, { description: `${selector} text ${JSON.stringify(expected)}`, args: [selector, expected instanceof RegExp ? { pattern: expected.source } : expected], ...options });
  }

  async #center(selector) {
    await this.waitVisible(selector);
    const sample = () => this.evaluate((sel) => {
      const element = document.querySelector(sel);
      element.scrollIntoView({ block: "center", inline: "center" });
      const rect = element.getBoundingClientRect();
      const owns = (top) => top === element || element.contains(top) || !!top?.closest("label")?.contains(element);
      const points = [];
      for (let row = 1; row <= 9; row += 1) for (let column = 1; column <= 9; column += 1) points.push([rect.left + rect.width * column / 10, rect.top + rect.height * row / 10]);
      points.sort((a, b) => Math.hypot(a[0] - rect.left - rect.width / 2, a[1] - rect.top - rect.height / 2) - Math.hypot(b[0] - rect.left - rect.width / 2, b[1] - rect.top - rect.height / 2));
      const found = points.find(([px, py]) => px >= 0 && py >= 0 && px < innerWidth && py < innerHeight && owns(document.elementFromPoint(px, py)));
      const centre = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
      const describe = (node) => node && `${node.tagName.toLowerCase()}${node.id ? `#${node.id}` : ""}${node.className && typeof node.className === "string" ? `.${node.className.split(" ").join(".")}` : ""}`;
      return { x: found?.[0], y: found?.[1], hit: !!found, disabled: !!element.disabled, rect: [rect.left, rect.top, rect.width, rect.height].map(Math.round), covering: describe(centre), scrollY: Math.round(scrollY) };
    }, selector);
    const deadline = Date.now() + 5000;
    let previous = await sample();
    for (;;) {
      await sleep(40);
      const current = await sample();
      if (current.hit && previous.hit && current.x === previous.x && current.y === previous.y) {
        if (current.disabled) throw new Error(`${selector} is disabled`);
        return current;
      }
      if (Date.now() > deadline) throw new Error(`${selector} is covered by another element or still moving: ${JSON.stringify([previous, current])}`);
      previous = current;
    }
  }

  async click(selector) {
    const { x, y } = await this.#center(selector);
    for (const [type, extra] of [["mouseMoved", {}], ["mousePressed", { button: "left", clickCount: 1 }], ["mouseReleased", { button: "left", clickCount: 1 }]]) {
      await this.send("Input.dispatchMouseEvent", { type, x, y, ...extra });
    }
  }

  async openDetails(detailsSelector) {
    const isOpen = () => this.evaluate((sel) => document.querySelector(sel).open, detailsSelector);
    const deadline = Date.now() + 20000;
    while (!(await isOpen())) {
      if (Date.now() > deadline) throw new Error(`${detailsSelector} would not open`);
      await this.click(`${detailsSelector} > summary`);
      const settle = Date.now() + 1500;
      while (!(await isOpen()) && Date.now() < settle) await sleep(30);
    }
  }

  async setChecked(selector, wanted) {
    if ((await this.checked(selector)) !== wanted) await this.click(selector);
    if ((await this.checked(selector)) !== wanted) throw new Error(`${selector} did not become ${wanted ? "checked" : "unchecked"}`);
  }

  async press(key) {
    const definition = KEYS[key];
    await this.send("Input.dispatchKeyEvent", { type: definition.text ? "keyDown" : "rawKeyDown", ...definition });
    await this.send("Input.dispatchKeyEvent", { type: "keyUp", ...definition });
  }

  async type(selector, text) {
    await this.click(selector);
    await this.evaluate((sel) => document.querySelector(sel).select(), selector);
    if (text === "") await this.press("Backspace");
    else await this.send("Input.insertText", { text: String(text) });
  }

  async selectValue(selector, value) {
    const read = () => this.evaluate((sel) => { const element = document.querySelector(sel); return { value: element.value, labels: Array.from(element.options, (option) => option.value) }; }, selector);
    if (!(await read()).labels.includes(value)) throw new Error(`${selector} has no option ${value}`);
    await this.evaluate((sel) => document.querySelector(sel).focus(), selector);
    for (let attempt = 0; attempt < 2 && (await read()).value !== value; attempt++) {
      if (attempt) await sleep(1100);
      for (const char of value) {
        const code = char.toUpperCase().charCodeAt(0);
        await this.send("Input.dispatchKeyEvent", { type: "keyDown", key: char, code: `Key${char.toUpperCase()}`, text: char, windowsVirtualKeyCode: code });
        await this.send("Input.dispatchKeyEvent", { type: "keyUp", key: char, code: `Key${char.toUpperCase()}`, windowsVirtualKeyCode: code });
      }
    }
    if ((await read()).value !== value) throw new Error(`${selector} did not reach ${value} through the keyboard`);
  }

  assertClean() {
    if (this.errors.length) throw new Error(`browser reported problems:\n${this.errors.join("\n")}`);
  }

  async close() {
    await this.connection.send("Target.disposeBrowserContext", { browserContextId: this.contextId }).catch(() => {});
  }
}

export class Chrome {
  constructor(process, userData, connection) {
    this.process = process;
    this.userData = userData;
    this.connection = connection;
  }

  async newPage() {
    const { browserContextId } = await this.connection.send("Target.createBrowserContext");
    const { targetId } = await this.connection.send("Target.createTarget", { url: "about:blank", browserContextId });
    const { sessionId } = await this.connection.send("Target.attachToTarget", { targetId, flatten: true });
    const page = new Page(this.connection, sessionId, targetId, browserContextId);
    await page.init();
    return page;
  }

  async close() {
    this.connection.socket.close();
    const running = () => this.process.exitCode === null && this.process.signalCode === null;
    const exited = running() ? new Promise((resolve) => this.process.once("exit", resolve)) : Promise.resolve();
    killGroup(this.process, "SIGTERM");
    await Promise.race([exited, sleep(5000)]);
    killGroup(this.process, "SIGKILL");
    await Promise.race([exited, sleep(5000)]);
    try {
      rmSync(this.userData, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
    } catch (error) {
      process.emitWarning(`left Chrome profile ${this.userData}: ${error.message}`);
    }
  }
}

// Chrome's helpers share its process group, so they all stop before the profile is removed.
function killGroup(child, signal) {
  try {
    process.kill(-child.pid, signal);
  } catch {}
}

export async function launchChrome(binary) {
  const userData = mkdtempSync(join(tmpdir(), "ui-chrome-"));
  const child = spawn(binary, [
    "--headless=new", "--remote-debugging-port=0", `--user-data-dir=${userData}`, "--no-first-run",
    "--no-default-browser-check", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "about:blank",
  ], { stdio: "ignore", detached: true });
  process.once("exit", () => killGroup(child, "SIGKILL"));
  const portFile = join(userData, "DevToolsActivePort");
  const fail = (message) => {
    killGroup(child, "SIGKILL");
    throw new Error(message);
  };
  const deadline = Date.now() + 20000;
  let endpoint;
  while (!endpoint) {
    if (child.exitCode !== null) fail(`Chrome exited with ${child.exitCode}`);
    if (Date.now() > deadline) fail("Chrome did not publish its DevTools port");
    const [port, path] = existsSync(portFile) ? readFileSync(portFile, "utf8").split("\n") : [];
    if (port && path) endpoint = `ws://127.0.0.1:${port}${path}`;
    else await sleep(25);
  }
  const socket = new WebSocket(endpoint);
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("DevTools connection timed out")), 20000);
    socket.addEventListener("open", () => { clearTimeout(timer); resolve(); }, { once: true });
    socket.addEventListener("error", () => { clearTimeout(timer); reject(new Error("DevTools connection failed")); }, { once: true });
  }).catch((error) => fail(error.message));
  return new Chrome(child, userData, new Connection(socket));
}
