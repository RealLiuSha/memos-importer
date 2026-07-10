import { randomBytes } from "node:crypto";
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { createWriteStream } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import net from "node:net";
import { spawn } from "node:child_process";

const root = path.resolve(new URL("..", import.meta.url).pathname);
const dist = path.join(root, "web", "dist");
const chromePath = process.env.CHROME_PATH || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const screenshotPath = path.join(root, "tmp", "ui-smoke.png");
const mobileScreenshotPath = path.join(root, "tmp", "ui-smoke-mobile.png");
const accessPassword = "smoke-password";
const sessionCookie = "memos_importer_session=smoke-session";

const state = {
  config: {
    memos_endpoint: "",
    memos_token: "",
    notion_token: "",
    notion_time_source: "created_time",
    worker_count: 6,
  },
  jobs: [],
  jobDetail: new Map(),
  retryCount: 0,
  lastJobRequest: null,
  sse: new Map(),
  scheduled: new Set(),
};

const itemWarnings = JSON.stringify([
  { code: "unsupported_block", message: "unsupported Notion block type: heading_4", block_id: "block-1", severity: "warning" },
  { code: "unsupported_block", message: "unsupported Notion block type: heading_4 in nested content with a long warning message that must remain inspectable from the job detail table", block_id: "block-2", severity: "warning" },
]);

function json(res, value, status = 200) {
  const body = JSON.stringify(value);
  res.writeHead(status, {
    "content-type": "application/json",
    "content-length": Buffer.byteLength(body),
  });
  res.end(body);
}

function mask(value) {
  if (!value) return "";
  if (value.length <= 8) return "********";
  return `${value.slice(0, 4)}...${value.slice(-4)}`;
}

function assertBrowserConfig(config, label) {
  if (config?.memos_endpoint !== "http://memos.local") {
    throw new Error(`${label}: missing memos endpoint in browser config`);
  }
  if (config?.memos_token !== "memos-secret-token") {
    throw new Error(`${label}: missing memos token in browser config`);
  }
  if (config?.notion_token !== "notion-secret-token") {
    throw new Error(`${label}: missing Notion token in browser config`);
  }
  if (config?.notion_time_source !== "created_time") {
    throw new Error(`${label}: missing time source in browser config`);
  }
  if (config?.worker_count !== 6) {
    throw new Error(`${label}: missing worker count in browser config`);
  }
}

function isAuthenticated(req) {
  return (req.headers.cookie || "").split(/;\s*/).includes(sessionCookie);
}

async function readBody(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const raw = Buffer.concat(chunks).toString("utf8");
  return raw ? JSON.parse(raw) : {};
}

function sendEvent(jobID, type, payload = {}) {
  const res = state.sse.get(jobID);
  if (!res) return;
  res.write(`event: ${type}\n`);
  res.write(`data: ${JSON.stringify({ job_id: jobID, type, payload })}\n\n`);
}

function summarizeItems(items = []) {
  const summary = {
    total: items.length,
    pending: 0,
    running: 0,
    imported: 0,
    overwritten: 0,
    skipped: 0,
    failed: 0,
    completed: 0,
    progress_percent: 0,
  };
  for (const item of items) {
    if (item.status === "pending") summary.pending += 1;
    if (item.status === "running") summary.running += 1;
    if (item.status === "imported") summary.imported += 1;
    if (item.status === "overwritten") summary.overwritten += 1;
    if (item.status === "skipped") summary.skipped += 1;
    if (item.status === "failed") summary.failed += 1;
  }
  summary.completed = summary.imported + summary.overwritten + summary.skipped + summary.failed;
  summary.progress_percent = summary.total ? Math.floor(summary.completed * 100 / summary.total) : 0;
  return summary;
}

function jobWithSummary(detail) {
  return { ...detail.job, summary: summarizeItems(detail.items) };
}

function scheduleJobFailure(jobID) {
  setTimeout(() => {
    const detail = state.jobDetail.get(jobID);
    if (!detail || detail.job.status !== "running") return;
    detail.job.status = "failed";
    detail.items[0].status = "failed";
    detail.items[0].error_stage = "create_memo";
    detail.items[0].error = "simulated item failure";
    sendEvent(jobID, "item_failed", detail.items[0]);
    sendEvent(jobID, "job_failed", { failures: 1 });
  }, 150);
}

function scheduleJobSuccess(jobID) {
  setTimeout(() => {
    const detail = state.jobDetail.get(jobID);
    if (!detail) return;
    detail.job.status = "done";
    detail.items[0].status = "imported";
    detail.items[0].error_stage = "";
    detail.items[0].error = "";
    sendEvent(jobID, "item_imported", detail.items[0]);
    sendEvent(jobID, "job_done", {});
  }, 150);
}

async function handleAPI(req, res, url) {
  if (req.method === "POST" && url.pathname === "/api/session") {
    const body = await readBody(req);
    if (body.password !== accessPassword) {
      json(res, { error: "invalid access password" }, 401);
      return true;
    }
    const response = JSON.stringify({ authenticated: true });
    res.writeHead(200, {
      "content-type": "application/json",
      "content-length": Buffer.byteLength(response),
      "set-cookie": `${sessionCookie}; Path=/; HttpOnly; SameSite=Strict`,
    });
    res.end(response);
    return true;
  }
  if (!isAuthenticated(req)) {
    json(res, { error: "invalid access password" }, 401);
    return true;
  }
  if (req.method === "GET" && url.pathname === "/api/config") {
    json(res, {
      memos_endpoint: state.config.memos_endpoint,
      memos_token: mask(state.config.memos_token),
      notion_token: mask(state.config.notion_token),
      notion_time_source: state.config.notion_time_source,
      worker_count: state.config.worker_count,
    });
    return true;
  }
  if (req.method === "POST" && url.pathname === "/api/config") {
    const body = await readBody(req);
    state.config = { ...state.config, ...body };
    json(res, {
      memos_endpoint: state.config.memos_endpoint,
      memos_token: mask(state.config.memos_token),
      notion_token: mask(state.config.notion_token),
      notion_time_source: state.config.notion_time_source,
      worker_count: state.config.worker_count,
    });
    return true;
  }
  if (req.method === "POST" && url.pathname === "/api/config/verify") {
    const body = await readBody(req);
    assertBrowserConfig(body, "verify config");
    json(res, {
      memos: {
        ok: true,
        profile: { version: "0.29.1" },
        content_length_limit: 4096,
        settings_ok: true,
        error: "",
        settings_error: "",
      },
      notion: { ok: true, error: "" },
    });
    return true;
  }
  if (req.method === "POST" && url.pathname === "/api/sources/notion/tree") {
    const body = await readBody(req);
    assertBrowserConfig(body.config, "notion tree");
    json(res, {
      documents: [
        { source: "notion", id: "page-1", title: "Smoke Page", kind: "page", updated_at: "2024-01-02T00:00:00Z" },
        { source: "notion", id: "page-child", title: "Smoke Child", kind: "page", parent_id: "page-1", updated_at: "2024-01-02T12:00:00Z" },
        { source: "notion", id: "db-1", title: "Smoke Database", kind: "database", updated_at: "2024-01-03T00:00:00Z" },
      ],
    });
    return true;
  }
  if (req.method === "POST" && url.pathname === "/api/sources/notion/documents/page-1/preview") {
    const body = await readBody(req);
    assertBrowserConfig(body.config, "notion preview");
    json(res, {
      document: { source: "notion", id: "page-1", title: "Smoke Page", updated_at: "2024-01-02T00:00:00Z" },
      markdown: "Body\n\n<!-- Unsupported Notion block: synced_block -->",
      warnings: [{ code: "unsupported_block", message: "unsupported Notion block type: synced_block", block_id: "block-1", severity: "warning" }],
      attachment_count: 1,
      content_length: 64,
      content_length_limit: 4096,
      over_limit: false,
    });
    return true;
  }
  if (req.method === "POST" && url.pathname === "/api/jobs") {
    const body = await readBody(req);
    state.lastJobRequest = body;
    assertBrowserConfig(body.config, "create job");
    const ids = Array.isArray(body.external_ids) ? body.external_ids : [];
    if (!ids.includes("page-1") || !ids.includes("db-1")) {
      json(res, { error: `expected page and database selection, got ${JSON.stringify(ids)}` }, 400);
      return true;
    }
    if (body.options?.worker_count !== 6) {
      json(res, { error: `expected configured worker_count 6, got ${body.options?.worker_count}` }, 400);
      return true;
    }
    if (body.options?.time_source !== "created_time") {
      json(res, { error: `expected time_source created_time, got ${body.options?.time_source}` }, 400);
      return true;
    }
    if (body.options?.visibility !== "PUBLIC") {
      json(res, { error: `expected visibility PUBLIC, got ${body.options?.visibility}` }, 400);
      return true;
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
    const id = "job-1";
    const now = Date.now();
    const detail = {
      job: { id, source: "notion", status: "running", created_at: new Date().toISOString() },
      items: [{ external_id: "page-1", title: "Smoke Page", status: "running", warnings: itemWarnings, error_stage: "", error: "" }],
    };
    const history = Array.from({ length: 8 }, (_, index) => {
      const jobID = `job-history-${index + 1}`;
      return {
        job: { id: jobID, source: "notion", status: "done", created_at: new Date(now - (index + 1) * 60000).toISOString() },
        items: [
          { external_id: `history-page-${index + 1}`, title: `History Page ${index + 1}`, status: "imported", warnings: itemWarnings, error_stage: "", error: "" },
          { external_id: `history-page-${index + 1}-b`, title: `History Page ${index + 1} B`, status: "skipped", warnings: "", error_stage: "", error: "" },
        ],
      };
    });
    state.jobDetail.clear();
    for (const jobDetail of [detail, ...history]) state.jobDetail.set(jobDetail.job.id, jobDetail);
    state.jobs = Array.from(state.jobDetail.values()).map((jobDetail) => jobDetail.job);
    json(res, { job_id: id }, 202);
    return true;
  }
  if (req.method === "GET" && url.pathname === "/api/jobs") {
    json(res, { jobs: Array.from(state.jobDetail.values()).map(jobWithSummary) });
    return true;
  }
  const jobMatch = url.pathname.match(/^\/api\/jobs\/([^/]+)$/);
  if (req.method === "GET" && jobMatch) {
    const detail = state.jobDetail.get(jobMatch[1]);
    json(res, detail ? { ...detail, summary: summarizeItems(detail.items) } : { error: "not found" }, detail ? 200 : 404);
    return true;
  }
  const retryMatch = url.pathname.match(/^\/api\/jobs\/([^/]+)\/retry$/);
  if (req.method === "POST" && retryMatch) {
    const body = await readBody(req);
    assertBrowserConfig(body.config, "retry job");
    state.retryCount += 1;
    const detail = state.jobDetail.get(retryMatch[1]);
    if (detail) {
      detail.job.status = "running";
      detail.items[0].status = "running";
      detail.items[0].error_stage = "";
      detail.items[0].error = "";
      state.scheduled.delete(retryMatch[1]);
    }
    json(res, { job_id: retryMatch[1] }, 202);
    return true;
  }
  const resumeMatch = url.pathname.match(/^\/api\/jobs\/([^/]+)\/resume$/);
  if (req.method === "POST" && resumeMatch) {
    const body = await readBody(req);
    assertBrowserConfig(body.config, "resume job");
    const detail = state.jobDetail.get(resumeMatch[1]);
    if (detail) {
      detail.job.status = "running";
      detail.items[0].status = "running";
      detail.items[0].error_stage = "";
      detail.items[0].error = "";
      state.scheduled.delete(resumeMatch[1]);
    }
    json(res, { job_id: resumeMatch[1] }, 202);
    return true;
  }
  const cancelMatch = url.pathname.match(/^\/api\/jobs\/([^/]+)\/cancel$/);
  if (req.method === "POST" && cancelMatch) {
    json(res, { status: "not_running" });
    return true;
  }
  const eventsMatch = url.pathname.match(/^\/api\/jobs\/([^/]+)\/events$/);
  if (req.method === "GET" && eventsMatch) {
    const jobID = eventsMatch[1];
    res.writeHead(200, {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
      connection: "keep-alive",
    });
    res.write(`event: ready\ndata: {"job_id":"${jobID}"}\n\n`);
    state.sse.set(jobID, res);
    const detail = state.jobDetail.get(jobID);
    if (detail?.job.status === "running" && !state.scheduled.has(jobID)) {
      state.scheduled.add(jobID);
      if (state.retryCount > 0) scheduleJobSuccess(jobID);
      else scheduleJobFailure(jobID);
    }
    req.on("close", () => {
      if (state.sse.get(jobID) === res) state.sse.delete(jobID);
    });
    return true;
  }
  return false;
}

async function serveStatic(res, pathname) {
  let file = pathname === "/" ? "/index.html" : pathname;
  file = path.normalize(file).replace(/^(\.\.[/\\])+/, "");
  const full = path.join(dist, file);
  try {
    const data = await readFile(full);
    const ext = path.extname(full);
    const type = ext === ".html" ? "text/html" : ext === ".js" ? "text/javascript" : ext === ".css" ? "text/css" : "application/octet-stream";
    res.writeHead(200, { "content-type": type });
    res.end(data);
  } catch {
    const data = await readFile(path.join(dist, "index.html"));
    res.writeHead(200, { "content-type": "text/html" });
    res.end(data);
  }
}

async function startServer() {
  const server = createServer(async (req, res) => {
    try {
      const url = new URL(req.url || "/", "http://127.0.0.1");
      if (url.pathname.startsWith("/api/") && await handleAPI(req, res, url)) return;
      await serveStatic(res, url.pathname);
    } catch (err) {
      json(res, { error: String(err?.stack || err) }, 500);
    }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  return { server, url: `http://127.0.0.1:${address.port}/` };
}

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
