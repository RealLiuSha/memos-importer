import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import path from "node:path";

const root = path.resolve(new URL("..", import.meta.url).pathname);
const dist = path.join(root, "web", "dist");

export const accessPassword = "smoke-password";
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

export async function startServer() {
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
