import { randomBytes } from "node:crypto";
import { readFile } from "node:fs/promises";
import { createWriteStream } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import net from "node:net";
import { spawn } from "node:child_process";

import { accessPassword, startServer } from "./ui-smoke-server.mjs";

const root = path.resolve(new URL("..", import.meta.url).pathname);
const dist = path.join(root, "web", "dist");
const chromePath = process.env.CHROME_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const screenshotPath = path.join(root, "tmp", "ui-smoke.png");
const mobileScreenshotPath = path.join(root, "tmp", "ui-smoke-mobile.png");

function wsFrame(payload) {
  const data = Buffer.from(payload);
  const mask = randomBytes(4);
  let header;
  if (data.length < 126) {
    header = Buffer.from([0x81, 0x80 | data.length]);
  } else if (data.length < 65536) {
    header = Buffer.alloc(4);
    header[0] = 0x81;
    header[1] = 0x80 | 126;
    header.writeUInt16BE(data.length, 2);
  } else {
    header = Buffer.alloc(10);
    header[0] = 0x81;
    header[1] = 0x80 | 127;
    header.writeBigUInt64BE(BigInt(data.length), 2);
  }
  const masked = Buffer.alloc(data.length);
  for (let i = 0; i < data.length; i++) masked[i] = data[i] ^ mask[i % 4];
  return Buffer.concat([header, mask, masked]);
}

function parseFrames(buffer) {
  const messages = [];
  let offset = 0;
  while (buffer.length - offset >= 2) {
    const first = buffer[offset];
    const second = buffer[offset + 1];
    let length = second & 0x7f;
    let headerLength = 2;
    if (length === 126) {
      if (buffer.length - offset < 4) break;
      length = buffer.readUInt16BE(offset + 2);
      headerLength = 4;
    } else if (length === 127) {
      if (buffer.length - offset < 10) break;
      length = Number(buffer.readBigUInt64BE(offset + 2));
      headerLength = 10;
    }
    const masked = (second & 0x80) !== 0;
    const maskLength = masked ? 4 : 0;
    const frameLength = headerLength + maskLength + length;
    if (buffer.length - offset < frameLength) break;
    let payload = buffer.subarray(offset + headerLength + maskLength, offset + frameLength);
    if (masked) {
      const mask = buffer.subarray(offset + headerLength, offset + headerLength + 4);
      payload = Buffer.from(payload.map((byte, i) => byte ^ mask[i % 4]));
    }
    const opcode = first & 0x0f;
    if (opcode === 1) messages.push(payload.toString("utf8"));
    offset += frameLength;
  }
  return { messages, rest: buffer.subarray(offset) };
}

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 1;
    this.pending = new Map();
    this.buffer = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      this.buffer = Buffer.concat([this.buffer, chunk]);
      const parsed = parseFrames(this.buffer);
      this.buffer = parsed.rest;
      for (const msg of parsed.messages) this.handle(JSON.parse(msg));
    });
  }
  handle(msg) {
    if (msg.id && this.pending.has(msg.id)) {
      const { resolve, reject } = this.pending.get(msg.id);
      this.pending.delete(msg.id);
      if (msg.error) reject(new Error(JSON.stringify(msg.error)));
      else resolve(msg.result || {});
    }
  }
  send(method, params = {}) {
    const id = this.nextID++;
    const payload = JSON.stringify({ id, method, params });
    this.socket.write(wsFrame(payload));
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      setTimeout(() => {
        if (this.pending.has(id)) {
          this.pending.delete(id);
          reject(new Error(`CDP timeout: ${method}`));
        }
      }, 10000).unref();
    });
  }
  close() {
    this.socket.end();
  }
}

async function connectCDP(wsURL) {
  const u = new URL(wsURL);
  const socket = net.createConnection({ host: u.hostname, port: Number(u.port) });
  await new Promise((resolve, reject) => {
    socket.once("connect", resolve);
    socket.once("error", reject);
  });
  const key = randomBytes(16).toString("base64");
  socket.write([
    `GET ${u.pathname}${u.search} HTTP/1.1`,
    `Host: ${u.host}`,
    "Upgrade: websocket",
    "Connection: Upgrade",
    `Sec-WebSocket-Key: ${key}`,
    "Sec-WebSocket-Version: 13",
    "\r\n",
  ].join("\r\n"));
  let handshake = Buffer.alloc(0);
  while (!handshake.includes(Buffer.from("\r\n\r\n"))) {
    handshake = Buffer.concat([handshake, await onceData(socket)]);
  }
  if (!handshake.toString("utf8").startsWith("HTTP/1.1 101")) {
    throw new Error(`WebSocket handshake failed: ${handshake.toString("utf8")}`);
  }
  const restIndex = handshake.indexOf("\r\n\r\n") + 4;
  const cdp = new CDP(socket);
  cdp.buffer = handshake.subarray(restIndex);
  return cdp;
}

function onceData(socket) {
  return new Promise((resolve, reject) => {
    socket.once("data", resolve);
    socket.once("error", reject);
  });
}

async function waitForChrome(port) {
  const base = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 80; i++) {
    try {
      const res = await fetch(`${base}/json/version`);
      if (res.ok) return base;
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("Chrome remote debugging endpoint did not start");
}

async function newTarget(base, pageURL) {
  let res = await fetch(`${base}/json/new?${encodeURIComponent(pageURL)}`, { method: "PUT" });
  if (!res.ok) res = await fetch(`${base}/json/new?${encodeURIComponent(pageURL)}`);
  if (!res.ok) throw new Error(`failed to create Chrome target: ${res.status}`);
  return res.json();
}

async function evalJS(cdp, expression) {
  const result = await cdp.send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
  return result.result?.value;
}

async function waitFor(cdp, expression, label, timeout = 5000) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    try {
      last = await evalJS(cdp, expression);
      if (last) return last;
    } catch (err) {
      last = err.message;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out waiting for ${label}: ${last}`);
}

async function setViewport(cdp, width, height, mobile = false) {
  await cdp.send("Emulation.setDeviceMetricsOverride", {
    width,
    height,
    deviceScaleFactor: 1,
    mobile,
  });
}

async function assertNoPageOverflow(cdp, label) {
  const result = await evalJS(cdp, `
    (() => {
      const doc = document.documentElement;
      const body = document.body;
      const pageWidth = Math.max(doc.scrollWidth, body.scrollWidth);
      const clientWidth = doc.clientWidth;
      const overflowingButtons = Array.from(document.querySelectorAll("button"))
        .filter((el) => el.scrollWidth > el.clientWidth + 1)
        .map((el) => el.textContent.trim())
        .slice(0, 5);
      return {
        ok: pageWidth <= clientWidth + 1 && overflowingButtons.length === 0,
        pageWidth,
        clientWidth,
        overflowingButtons,
      };
    })()
  `);
  if (!result.ok) {
    throw new Error(`${label} viewport overflow: ${JSON.stringify(result)}`);
  }
}

async function captureScreenshot(cdp, filePath) {
  const shot = await cdp.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
  await new Promise((resolve, reject) => {
    const out = createWriteStream(filePath);
    out.on("finish", resolve);
    out.on("error", reject);
    out.end(Buffer.from(shot.data, "base64"));
  });
}

function setInput(selector, value) {
  return `
    (() => {
      const el = document.querySelector(${JSON.stringify(selector)});
      const proto = el instanceof HTMLTextAreaElement
        ? HTMLTextAreaElement.prototype
        : el instanceof HTMLSelectElement
          ? HTMLSelectElement.prototype
          : HTMLInputElement.prototype;
      const setter = Object.getOwnPropertyDescriptor(proto, "value").set;
      setter.call(el, ${JSON.stringify(value)});
      el.dispatchEvent(new Event("input", { bubbles: true }));
      el.dispatchEvent(new Event("change", { bubbles: true }));
      return el.value;
    })()
  `;
}

function click(selector) {
  return `document.querySelector(${JSON.stringify(selector)}).click()`;
}

async function main() {
  await readFile(path.join(dist, "index.html"));
  const { server, url } = await startServer();
  const chromePort = 41000 + Math.floor(Math.random() * 1000);
  const userDataDir = await mkdtemp(path.join(tmpdir(), "memos-importer-chrome-"));
  const chrome = spawn(chromePath, [
    "--headless=new",
    "--disable-gpu",
    "--no-first-run",
    "--no-default-browser-check",
    `--remote-debugging-port=${chromePort}`,
    `--user-data-dir=${userDataDir}`,
    "about:blank",
  ], { stdio: ["ignore", "ignore", "pipe"] });
  let cdp;
  try {
    const chromeBase = await waitForChrome(chromePort);
    const target = await newTarget(chromeBase, url);
    cdp = await connectCDP(target.webSocketDebuggerUrl);
    await cdp.send("Runtime.enable");
    await cdp.send("Page.enable");
    await setViewport(cdp, 1280, 900);
    await waitFor(cdp, "!!document.querySelector('[data-testid=\"save-config\"]')", "app shell");
    await waitFor(cdp, "!!document.querySelector('[data-testid=\"access-password\"]')", "access password prompt");
    await evalJS(cdp, setInput("[data-testid='access-password']", accessPassword));
    await evalJS(cdp, click("[data-testid='unlock-access']"));
    await waitFor(cdp, "document.querySelector('[data-testid=\"memos-endpoint\"]') && !document.querySelector('[data-testid=\"access-password\"]')", "unlocked config");

    await evalJS(cdp, setInput("[data-testid='memos-endpoint']", "http://memos.local"));
    await evalJS(cdp, setInput("[data-testid='memos-token']", "memos-secret-token"));
    await evalJS(cdp, setInput("[data-testid='notion-token']", "notion-secret-token"));
    await evalJS(cdp, setInput("[data-testid='time-source']", "created_time"));
    await evalJS(cdp, setInput("[data-testid='visibility']", "PUBLIC"));
    await evalJS(cdp, setInput("[data-testid='workers']", "6"));
    await evalJS(cdp, click("[data-testid='save-config']"));
    await waitFor(cdp, "document.body.innerText.includes('配置已保存') || document.body.innerText.includes('Config saved')", "saved config");
    await waitFor(cdp, `
      (() => {
        const saved = JSON.parse(localStorage.getItem('memos-importer.config.v1') || '{}');
        return saved.memos_token === 'memos-secret-token' && saved.notion_token === 'notion-secret-token' && saved.worker_count === 6;
      })()
    `, "browser-local config");

    await evalJS(cdp, click("[data-testid='verify-config']"));
    await waitFor(cdp, "document.body.innerText.includes('v0.29.1') && document.body.innerText.includes('limit 4096')", "config verify");

    await evalJS(cdp, click("[data-testid='load-documents']"));
    await waitFor(cdp, "!!document.querySelector('[data-testid=\"doc-page-1\"]')", "document list");
    await waitFor(cdp, "document.body.innerText.includes('数据库') || document.body.innerText.includes('database')", "document kind badge");
    await waitFor(cdp, `
      (() => {
        const parent = document.querySelector('[data-testid="doc-page-1"]')?.closest('.doc-row')?.querySelector('.doc-title');
        const child = document.querySelector('[data-testid="doc-page-child"]')?.closest('.doc-row')?.querySelector('.doc-title');
        return !!parent && !!child && child.getBoundingClientRect().left > parent.getBoundingClientRect().left + 8;
      })()
    `, "document tree indentation");
    await evalJS(cdp, setInput("[data-testid='document-search']", "database"));
    await waitFor(cdp, "!document.querySelector('[data-testid=\"doc-page-1\"]') && !!document.querySelector('[data-testid=\"doc-db-1\"]')", "document search filter");
    await evalJS(cdp, setInput("[data-testid='document-search']", ""));
    await waitFor(cdp, "!!document.querySelector('[data-testid=\"doc-page-1\"]') && !!document.querySelector('[data-testid=\"doc-db-1\"]')", "document search clear");
    await waitFor(cdp, "document.querySelector('[data-testid=\"preview-output\"]')?.innerText.includes('Unsupported Notion block')", "auto preview");
    await evalJS(cdp, click("[data-testid='doc-page-1']"));
    await evalJS(cdp, click("[data-testid='doc-db-1']"));
    await waitFor(cdp, "document.body.innerText.includes('unsupported_block') && document.querySelector('[data-testid=\"preview-output\"]').innerText.includes('Unsupported Notion block')", "preview warning");

    await evalJS(cdp, click("[data-testid='start-import']"));
    await waitFor(cdp, `
      (() => {
        const button = document.querySelector('[data-testid="start-import"]');
        return !!button && button.disabled && button.getAttribute('aria-busy') === 'true' && /导入中|Importing/.test(button.textContent);
      })()
    `, "import button loading");
    await waitFor(cdp, "document.body.innerText.includes('failed') || document.body.innerText.includes('失败')", "failed job event");
    await waitFor(cdp, "document.querySelectorAll('.job-card').length >= 8", "scrollable job history");
    const jobListLayout = await evalJS(cdp, `
      (() => {
        const panel = document.querySelector('.jobs-panel');
        const list = document.querySelector('.job-list');
        if (!panel || !list) return { ok: false, reason: 'missing jobs panel' };
        const panelRect = panel.getBoundingClientRect();
        const listRect = list.getBoundingClientRect();
        const style = getComputedStyle(list);
        return {
          ok: style.overflowY !== 'visible' && list.scrollHeight > list.clientHeight && listRect.bottom <= panelRect.bottom + 1,
          overflowY: style.overflowY,
          listScrollHeight: list.scrollHeight,
          listClientHeight: list.clientHeight,
          panelBottom: panelRect.bottom,
          listBottom: listRect.bottom,
        };
      })()
    `);
    if (!jobListLayout.ok) throw new Error(`job list layout overflow: ${JSON.stringify(jobListLayout)}`);
    await waitFor(cdp, "!!document.querySelector('[data-testid=\"open-job-1\"]')", "history job");
    await waitFor(cdp, "!!document.querySelector('[data-testid=\"resume-job-1\"]')", "resume button");
    await evalJS(cdp, click("[data-testid='open-job-1']"));
    await waitFor(cdp, "document.querySelector('[data-testid=\"job-detail\"]').innerText.includes('failed')", "failed detail");
    await waitFor(cdp, "document.querySelector('[data-testid=\"job-detail\"]').innerText.includes('unsupported_block')", "history warning");
    await waitFor(cdp, `
      (() => {
        const warning = document.querySelector('.warning-text');
        return !!warning && warning.getAttribute('title')?.includes('unsupported Notion block type: heading_4') && warning.textContent.includes('nested content');
      })()
    `, "full warning text");
    await evalJS(cdp, "document.querySelector('.warning-text')?.focus()");
    await waitFor(cdp, `
      (() => {
        const warning = document.querySelector('.warning-text');
        if (!warning) return false;
        const style = getComputedStyle(warning);
        return warning.clientHeight > 36 && style.overflowY !== 'hidden';
      })()
    `, "expanded warning text");
    await evalJS(cdp, click("[data-testid='retry-job-1']"));
    await waitFor(cdp, "document.querySelector('[data-testid=\"job-detail\"]').innerText.includes('imported')", "retry success event");
    await evalJS(cdp, click("[data-testid='open-job-1']"));
    await waitFor(cdp, "document.querySelector('[data-testid=\"job-detail\"]').innerText.includes('imported')", "imported detail");
    await waitFor(cdp, "document.querySelector('[data-testid=\"job-detail\"]').innerText.includes('unsupported_block')", "imported warning detail");

    await assertNoPageOverflow(cdp, "desktop");
    await captureScreenshot(cdp, screenshotPath);
    await setViewport(cdp, 390, 844, true);
    await new Promise((resolve) => setTimeout(resolve, 100));
    await assertNoPageOverflow(cdp, "mobile");
    await captureScreenshot(cdp, mobileScreenshotPath);
    console.log(`ui smoke passed: ${url}`);
    console.log(`screenshot: ${screenshotPath}`);
    console.log(`mobile screenshot: ${mobileScreenshotPath}`);
  } finally {
    if (cdp) cdp.close();
    server.close();
    chrome.kill("SIGTERM");
    await waitForExit(chrome, 3000).catch(() => {
      chrome.kill("SIGKILL");
      return waitForExit(chrome, 3000);
    }).catch(() => {});
    await rmRetry(userDataDir);
  }
}

function waitForExit(child, timeout) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("process exit timeout")), timeout);
    child.once("exit", () => {
      clearTimeout(timer);
      resolve();
    });
  });
}

async function rmRetry(dir) {
  let lastErr;
  for (let i = 0; i < 5; i++) {
    try {
      await rm(dir, { recursive: true, force: true });
      return;
    } catch (err) {
      lastErr = err;
      await new Promise((resolve) => setTimeout(resolve, 200));
    }
  }
  throw lastErr;
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
