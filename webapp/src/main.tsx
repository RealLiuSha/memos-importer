import React, { useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";

type Lang = "zh" | "en";
type StatusTone = "idle" | "checking" | "ok" | "error";
type TimeSourceMode = "created_time" | "last_edited_time" | "property";

type ConfigDraft = {
  memos_endpoint: string;
  memos_token: string;
  notion_token: string;
  worker_count: number;
};

type ConfigResponse = {
  memos_endpoint?: string;
  memos_token?: string;
  notion_token?: string;
  notion_time_source?: string;
  worker_count?: number;
};

type BrowserConfig = {
  memos_endpoint: string;
  memos_token: string;
  notion_token: string;
  notion_time_source: string;
  worker_count: number;
};

type VerifyResponse = {
  memos?: {
    ok?: boolean;
    profile?: { version?: string };
    content_length_limit?: number;
    settings_ok?: boolean;
    error?: string;
    settings_error?: string;
  };
  notion?: { ok?: boolean; error?: string };
};

type DocumentRef = {
  source: string;
  id: string;
  title: string;
  updated_at: string;
  parent_id?: string;
  kind?: string;
};

type Warning = { code?: string; message?: string; block_id?: string; severity?: string };

type PreviewResponse = {
  document: DocumentRef;
  markdown: string;
  warnings?: Warning[];
  attachment_count?: number;
  content_length?: number;
  content_length_limit?: number;
  over_limit?: boolean;
};

type JobSummary = {
  total: number;
  pending: number;
  running: number;
  imported: number;
  overwritten: number;
  skipped: number;
  failed: number;
  completed: number;
  progress_percent: number;
};

type Job = {
  id: string;
  status: string;
  source: string;
  options?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  error?: string;
  summary?: JobSummary;
};

type ImportItem = {
  external_id: string;
  title: string;
  status: string;
  warnings?: string;
  error_stage?: string;
  error?: string;
  memo_id?: string;
};

type JobDetail = { job: Job; items: ImportItem[]; summary?: JobSummary };
type DisplayDocument = DocumentRef & { depth: number };

class APIError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

const STRINGS = {
  zh: {
    appSubtitle: "Notion 导入 memos 控制台",
    configTitle: "配置",
    endpointLabel: "memos 端点",
    memosTokenLabel: "memos Token",
    notionTokenLabel: "Notion Token",
    timeSourceLabel: "时间来源",
    propertyLabel: "日期属性名",
    strategyLabel: "重复导入",
    visibilityLabel: "导入可见性",
    visibilityPrivate: "私有",
    visibilityProtected: "登录可见",
    visibilityPublic: "公开",
    workersLabel: "并发",
    saveBtn: "保存",
    verifyBtn: "校验",
    verifying: "校验中...",
    savedToast: "配置已保存到本地浏览器",
    statusTitle: "连接状态",
    statusIdle: "尚未校验",
    statusChecking: "校验中",
    statusOk: "已连接",
    statusError: "连接失败",
    jobsTitle: "导入任务",
    jobsEmpty: "暂无导入任务",
    jobDone: "已完成",
    jobFailed: "失败",
    jobRunning: "导入中",
    jobCanceled: "已取消",
    jobPending: "等待中",
    docsTitle: "文档",
    searchPlaceholder: "搜索文档标题...",
    loadBtn: "加载 Notion 文档",
    loadingBtn: "加载中...",
    importBtn: "导入到 memos",
    importingBtn: "导入中...",
    selected: "已选",
    listEmptyTitle: "还没有文档",
    listEmptyDesc: "点击加载拉取页面与数据库列表",
    previewEmptyTitle: "选择文档查看预览",
    previewEmptyDesc: "Markdown 内容与转换警告会显示在这里",
    warningsLabel: "转换警告",
    resultTitle: "任务明细",
    unlockTitle: "访问认证",
    unlockBtn: "解锁",
    passwordLabel: "访问密码",
    openBtn: "详情",
    cancelBtn: "取消",
    resumeBtn: "恢复",
    retryBtn: "重试失败项",
    docTypeDatabase: "数据库",
    docTypePage: "页面",
    noPreviewForDatabase: "数据库",
    overLimit: "内容超过 memos 限制",
    maskedToken: "已保存",
  },
  en: {
    appSubtitle: "Notion to memos import console",
    configTitle: "Config",
    endpointLabel: "memos endpoint",
    memosTokenLabel: "memos token",
    notionTokenLabel: "Notion token",
    timeSourceLabel: "Time source",
    propertyLabel: "Date property",
    strategyLabel: "Duplicate policy",
    visibilityLabel: "Import visibility",
    visibilityPrivate: "Private",
    visibilityProtected: "Signed-in",
    visibilityPublic: "Public",
    workersLabel: "Workers",
    saveBtn: "Save",
    verifyBtn: "Verify",
    verifying: "Verifying...",
    savedToast: "Config saved in this browser",
    statusTitle: "Connection status",
    statusIdle: "Not verified",
    statusChecking: "Checking",
    statusOk: "Connected",
    statusError: "Connection failed",
    jobsTitle: "Import jobs",
    jobsEmpty: "No import jobs yet",
    jobDone: "Done",
    jobFailed: "Failed",
    jobRunning: "Running",
    jobCanceled: "Canceled",
    jobPending: "Pending",
    docsTitle: "Documents",
    searchPlaceholder: "Search document titles...",
    loadBtn: "Load Notion docs",
    loadingBtn: "Loading...",
    importBtn: "Import to memos",
    importingBtn: "Importing...",
    selected: "Selected",
    listEmptyTitle: "No documents yet",
    listEmptyDesc: "Load pages and databases",
    previewEmptyTitle: "Select a document to preview",
    previewEmptyDesc: "Markdown and conversion warnings appear here",
    warningsLabel: "Conversion warnings",
    resultTitle: "Job detail",
    unlockTitle: "Access",
    unlockBtn: "Unlock",
    passwordLabel: "Access password",
    openBtn: "Open",
    cancelBtn: "Cancel",
    resumeBtn: "Resume",
    retryBtn: "Retry failed",
    docTypeDatabase: "database",
    docTypePage: "page",
    noPreviewForDatabase: "database",
    overLimit: "Content exceeds memos limit",
    maskedToken: "saved",
  },
};

const CONFIG_STORAGE_KEY = "memos-importer.config.v1";
const defaultBrowserConfig: BrowserConfig = {
  memos_endpoint: "",
  memos_token: "",
  notion_token: "",
  notion_time_source: "created_time",
  worker_count: 4,
};

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new APIError(body.error || res.statusText, res.status);
  return body;
}

function parseTimeSource(value: string | undefined): { mode: TimeSourceMode; property: string } {
  if (value?.startsWith("property:")) {
    return { mode: "property", property: value.slice("property:".length) };
  }
  if (value === "last_edited_time") return { mode: "last_edited_time", property: "" };
  return { mode: "created_time", property: "" };
}

function buildTimeSource(mode: TimeSourceMode, property: string): string {
  if (mode === "property") return `property:${property.trim()}`;
  return mode;
}

function readBrowserConfig(): BrowserConfig {
  try {
    const raw = window.localStorage.getItem(CONFIG_STORAGE_KEY);
    if (!raw) return defaultBrowserConfig;
    const parsed = JSON.parse(raw) as Partial<BrowserConfig>;
    return {
      ...defaultBrowserConfig,
      ...parsed,
      worker_count: Number(parsed.worker_count || defaultBrowserConfig.worker_count),
    };
  } catch {
    return defaultBrowserConfig;
  }
}

function writeBrowserConfig(value: BrowserConfig) {
  window.localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(value));
}

function arrangeDocuments(docs: DocumentRef[]): DisplayDocument[] {
  const byID = new Map(docs.map((doc) => [doc.id, doc]));
  const children = new Map<string, DocumentRef[]>();
  const roots: DocumentRef[] = [];
  for (const doc of docs) {
    if (doc.parent_id && byID.has(doc.parent_id)) {
      const list = children.get(doc.parent_id) || [];
      list.push(doc);
      children.set(doc.parent_id, list);
    } else {
      roots.push(doc);
    }
  }
  const sortRefs = (items: DocumentRef[]) => items.sort((a, b) => (a.title || a.id).localeCompare(b.title || b.id) || a.id.localeCompare(b.id));
  const arranged: DisplayDocument[] = [];
  const seen = new Set<string>();
  const visit = (doc: DocumentRef, depth: number) => {
    if (seen.has(doc.id)) return;
    seen.add(doc.id);
    arranged.push({ ...doc, depth });
    for (const child of sortRefs([...(children.get(doc.id) || [])])) visit(child, depth + 1);
  };
  for (const doc of sortRefs([...roots])) visit(doc, 0);
  for (const doc of sortRefs(docs.filter((doc) => !seen.has(doc.id)))) visit(doc, 0);
  return arranged;
}

function descendantsOf(docs: DocumentRef[], id: string): string[] {
  const result: string[] = [];
  const visit = (parentID: string) => {
    for (const doc of docs) {
      if (doc.parent_id !== parentID) continue;
      result.push(doc.id);
      visit(doc.id);
    }
  };
  visit(id);
  return result;
}

function parseWarnings(raw: string | undefined): Warning[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function formatWarning(warning: Warning): string {
  return [warning.code, warning.message].filter(Boolean).join(": ") || "warning";
}

function summarizeItems(items: ImportItem[]): JobSummary {
  const summary: JobSummary = {
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
  summary.progress_percent = summary.total > 0 ? Math.floor((summary.completed * 100) / summary.total) : 0;
  return summary;
}

function statusLabel(status: string, s: typeof STRINGS.zh): string {
  if (status === "done") return s.jobDone;
  if (status === "failed") return s.jobFailed;
  if (status === "running") return s.jobRunning;
  if (status === "canceled") return s.jobCanceled;
  return s.jobPending;
}

function statusClass(status: string): string {
  if (status === "done") return "ok";
  if (status === "failed") return "error";
  if (status === "running") return "running";
  if (status === "canceled") return "muted";
  return "pending";
}

function shortTime(value: string | undefined): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  return `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function App() {
  const initialConfig = readBrowserConfig();
  const initialTimeSource = parseTimeSource(initialConfig.notion_time_source);
  const [lang, setLang] = useState<Lang>("zh");
  const s = STRINGS[lang];
  const [config, setConfig] = useState<ConfigDraft>({
    memos_endpoint: initialConfig.memos_endpoint,
    memos_token: initialConfig.memos_token,
    notion_token: initialConfig.notion_token,
    worker_count: initialConfig.worker_count,
  });
  const [maskedTokens, setMaskedTokens] = useState({ memos: "", notion: "" });
  const [timeSourceMode, setTimeSourceMode] = useState<TimeSourceMode>(initialTimeSource.mode);
  const [timeSourceProperty, setTimeSourceProperty] = useState(initialTimeSource.property);
  const [strategy, setStrategy] = useState("skip");
  const [visibility, setVisibility] = useState("PRIVATE");
  const [accessPassword, setAccessPassword] = useState("");
  const [authRequired, setAuthRequired] = useState(false);
  const [verifyResult, setVerifyResult] = useState<VerifyResponse | null>(null);
  const [verifying, setVerifying] = useState(false);
  const [showSavedToast, setShowSavedToast] = useState(false);
  const [documents, setDocuments] = useState<DocumentRef[]>([]);
  const [docsLoadState, setDocsLoadState] = useState<"idle" | "loading" | "loaded">("idle");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [previewID, setPreviewID] = useState<string | null>(null);
  const [preview, setPreview] = useState<PreviewResponse | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [jobDetail, setJobDetail] = useState<JobDetail | null>(null);
  const [importing, setImporting] = useState(false);
  const [message, setMessage] = useState("");
  const eventSources = useRef<Map<string, EventSource>>(new Map());
  const importInFlight = useRef(false);

  useEffect(() => {
    loadConfig().catch(handleError);
    loadJobs().catch(handleError);
    return () => {
      eventSources.current.forEach((source) => source.close());
      eventSources.current.clear();
    };
  }, []);

  const visibleDocuments = useMemo(() => {
    const normalized = search.trim().toLowerCase();
    const filtered = normalized
      ? documents.filter((doc) => [doc.title, doc.id, doc.kind, doc.parent_id]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(normalized)))
      : documents;
    return arrangeDocuments(filtered);
  }, [documents, search]);

  const selectedIDs = useMemo(() => Object.keys(selected).filter((id) => selected[id]), [selected]);
  const totalPages = documents.filter((doc) => doc.kind !== "database").length;
  const selectedPages = documents.filter((doc) => doc.kind !== "database" && selected[doc.id]).length;
  const anyRunning = jobs.some((job) => job.status === "running");
  const selectedPreviewDoc = previewID ? documents.find((doc) => doc.id === previewID) : null;

  async function loadConfig() {
    const cfg = await api<ConfigResponse>("/api/config");
    setConfig((old) => ({
      ...old,
      memos_endpoint: old.memos_endpoint || cfg.memos_endpoint || "",
      worker_count: Number(old.worker_count || cfg.worker_count || 4),
    }));
    setMaskedTokens({ memos: cfg.memos_token || "", notion: cfg.notion_token || "" });
  }

  function configPayload() {
    const payload: Record<string, unknown> = {
      memos_endpoint: config.memos_endpoint,
      notion_time_source: buildTimeSource(timeSourceMode, timeSourceProperty),
      worker_count: config.worker_count,
    };
    if (config.memos_token.trim()) payload.memos_token = config.memos_token.trim();
    if (config.notion_token.trim()) payload.notion_token = config.notion_token.trim();
    return payload;
  }

  function configEnvelope() {
    return { config: configPayload() };
  }

  function saveConfig() {
    try {
      writeBrowserConfig({
        memos_endpoint: config.memos_endpoint,
        memos_token: config.memos_token,
        notion_token: config.notion_token,
        notion_time_source: buildTimeSource(timeSourceMode, timeSourceProperty),
        worker_count: config.worker_count,
      });
      setShowSavedToast(true);
      setTimeout(() => setShowSavedToast(false), 2200);
      setMessage("");
    } catch (err) {
      handleError(err);
    }
  }

  async function verifyConfig() {
    setVerifying(true);
    setVerifyResult(null);
    try {
      const data = await api<VerifyResponse>("/api/config/verify", { method: "POST", body: JSON.stringify(configPayload()) });
      setVerifyResult(data);
      setMessage("");
    } catch (err) {
      handleError(err);
    } finally {
      setVerifying(false);
    }
  }

  async function loadDocuments() {
    setDocsLoadState("loading");
    try {
      const data = await api<{ documents: DocumentRef[] }>("/api/sources/notion/tree", { method: "POST", body: JSON.stringify(configEnvelope()) });
      const docs = data.documents || [];
      setDocuments(docs);
      setDocsLoadState("loaded");
      setSelected({});
      setPreview(null);
      setPreviewID(docs[0]?.id || null);
      if (docs[0] && docs[0].kind !== "database") await loadPreview(docs[0].id);
      setMessage("");
    } catch (err) {
      setDocsLoadState("idle");
      handleError(err);
    }
  }

  async function loadPreview(id: string) {
    const doc = documents.find((item) => item.id === id);
    setPreviewID(id);
    setPreview(null);
    if (doc?.kind === "database") return;
    setPreviewLoading(true);
    try {
      const data = await api<PreviewResponse>(`/api/sources/notion/documents/${encodeURIComponent(id)}/preview`, { method: "POST", body: JSON.stringify(configEnvelope()) });
      setPreview(data);
      setMessage("");
    } catch (err) {
      handleError(err);
    } finally {
      setPreviewLoading(false);
    }
  }

  function toggleDocument(doc: DocumentRef, checked: boolean) {
    const ids = doc.kind === "database" ? [doc.id, ...descendantsOf(documents, doc.id)] : [doc.id];
    setSelected((old) => {
      const next = { ...old };
      for (const id of ids) next[id] = checked;
      return next;
    });
  }

  async function startImport() {
    if (selectedIDs.length === 0 || anyRunning || importing || importInFlight.current) return;
    if (timeSourceMode === "property" && !timeSourceProperty.trim()) {
      setMessage(lang === "zh" ? "请填写日期属性名" : "Enter a date property");
      return;
    }
    importInFlight.current = true;
    setImporting(true);
    try {
      const data = await api<{ job_id: string }>("/api/jobs", {
        method: "POST",
        body: JSON.stringify({
          source: "notion",
          external_ids: selectedIDs,
          config: configPayload(),
          options: {
            strategy,
            visibility,
            worker_count: config.worker_count,
            time_source: buildTimeSource(timeSourceMode, timeSourceProperty),
          },
        }),
      });
      subscribeJob(data.job_id);
      await loadJobs();
      await loadJob(data.job_id);
      setMessage("");
    } catch (err) {
      handleError(err);
    } finally {
      importInFlight.current = false;
      setImporting(false);
    }
  }

  function subscribeJob(jobID: string) {
    eventSources.current.get(jobID)?.close();
    const events = new EventSource(`/api/jobs/${encodeURIComponent(jobID)}/events`, { withCredentials: true });
    eventSources.current.set(jobID, events);
    const refresh = () => {
      loadJob(jobID).catch(handleError);
      loadJobs().catch(handleError);
    };
    ["item_running", "item_imported", "item_overwritten", "item_skipped", "item_failed", "item_canceled", "job_running", "job_done", "job_failed", "job_canceled"].forEach((name) => {
      events.addEventListener(name, () => {
        refresh();
        if (name === "job_done" || name === "job_failed" || name === "job_canceled") {
          events.close();
          eventSources.current.delete(jobID);
        }
      });
    });
  }

  async function loadJobs() {
    const data = await api<{ jobs: Job[] }>("/api/jobs");
    setJobs(data.jobs || []);
  }

  async function loadJob(id: string) {
    const data = await api<JobDetail>(`/api/jobs/${encodeURIComponent(id)}`);
    setJobDetail(data);
  }

  async function cancelJob(id: string) {
    try {
      await api(`/api/jobs/${encodeURIComponent(id)}/cancel`, { method: "POST" });
      await loadJob(id);
      await loadJobs();
    } catch (err) {
      handleError(err);
    }
  }

  async function retryJob(id: string) {
    try {
      await api(`/api/jobs/${encodeURIComponent(id)}/retry`, { method: "POST", body: JSON.stringify(configEnvelope()) });
      subscribeJob(id);
      await loadJob(id);
      await loadJobs();
    } catch (err) {
      handleError(err);
    }
  }

  async function resumeJob(id: string) {
    try {
      await api(`/api/jobs/${encodeURIComponent(id)}/resume`, { method: "POST", body: JSON.stringify(configEnvelope()) });
      subscribeJob(id);
      await loadJob(id);
      await loadJobs();
    } catch (err) {
      handleError(err);
    }
  }

  async function unlock() {
    try {
      await api<{ authenticated: boolean }>("/api/session", { method: "POST", body: JSON.stringify({ password: accessPassword }) });
      setAuthRequired(false);
      setAccessPassword("");
      await loadConfig();
      await loadJobs();
      setMessage("");
    } catch (err) {
      handleError(err);
    }
  }

  function handleError(err: unknown) {
    if (err instanceof APIError && err.status === 401) setAuthRequired(true);
    setMessage(err instanceof Error ? err.message : String(err));
  }

  function connectionStatus(kind: "memos" | "notion"): { tone: StatusTone; label: string; detail: string } {
    if (verifying) return { tone: "checking", label: s.statusChecking, detail: "" };
    if (!verifyResult) return { tone: "idle", label: s.statusIdle, detail: "" };
    if (kind === "memos") {
      const memos = verifyResult.memos || {};
      if (memos.ok && memos.settings_ok !== false) {
        return {
          tone: "ok",
          label: s.statusOk,
          detail: [`v${memos.profile?.version || "unknown"}`, memos.content_length_limit ? `limit ${memos.content_length_limit}` : ""].filter(Boolean).join(" · "),
        };
      }
      return { tone: "error", label: s.statusError, detail: memos.error || memos.settings_error || "" };
    }
    const notion = verifyResult.notion || {};
    if (notion.ok) return { tone: "ok", label: s.statusOk, detail: "authorized" };
    return { tone: "error", label: s.statusError, detail: notion.error || "" };
  }

  const memosStatus = connectionStatus("memos");
  const notionStatus = connectionStatus("notion");
  const importDisabled = selectedIDs.length === 0 || anyRunning || importing;

  return (
    <div className="app-shell">
      <header className="topbar">
        <div>
          <h1>memos-importer</h1>
          <p>{s.appSubtitle}</p>
        </div>
        <div className="segmented" aria-label="language">
          <button className={lang === "zh" ? "active" : ""} onClick={() => setLang("zh")}>中文</button>
          <button className={lang === "en" ? "active" : ""} onClick={() => setLang("en")}>EN</button>
        </div>
      </header>

      {authRequired && (
        <section className="auth-strip">
          <strong>{s.unlockTitle}</strong>
          <label>{s.passwordLabel}<input data-testid="access-password" type="password" value={accessPassword} onChange={(e) => setAccessPassword(e.target.value)} /></label>
          <button data-testid="unlock-access" onClick={unlock}>{s.unlockBtn}</button>
        </section>
      )}

      {message && <div className="message">{message}</div>}

      <main className="workspace">
        <aside className="sidebar">
          <section className="panel">
            <h2>{s.configTitle}</h2>
            <div className="field-stack">
              <label>{s.endpointLabel}<input data-testid="memos-endpoint" value={config.memos_endpoint} onChange={(e) => setConfig({ ...config, memos_endpoint: e.target.value })} /></label>
              <label>{s.memosTokenLabel}<input data-testid="memos-token" type="password" value={config.memos_token} placeholder={maskedTokens.memos ? s.maskedToken : ""} onChange={(e) => setConfig({ ...config, memos_token: e.target.value })} /></label>
              <label>{s.notionTokenLabel}<input data-testid="notion-token" type="password" value={config.notion_token} placeholder={maskedTokens.notion ? s.maskedToken : ""} onChange={(e) => setConfig({ ...config, notion_token: e.target.value })} /></label>
              <label>{s.timeSourceLabel}
                <select data-testid="time-source" value={timeSourceMode} onChange={(e) => setTimeSourceMode(e.target.value as TimeSourceMode)}>
                  <option value="created_time">created_time</option>
                  <option value="last_edited_time">last_edited_time</option>
                  <option value="property">property</option>
                </select>
              </label>
              {timeSourceMode === "property" && (
                <label>{s.propertyLabel}<input data-testid="time-source-property" value={timeSourceProperty} onChange={(e) => setTimeSourceProperty(e.target.value)} /></label>
              )}
              <div className="option-grid">
                <label>{s.strategyLabel}<select data-testid="strategy" value={strategy} onChange={(e) => setStrategy(e.target.value)}><option value="skip">skip</option><option value="overwrite">overwrite</option></select></label>
                <label>{s.visibilityLabel}<select data-testid="visibility" value={visibility} onChange={(e) => setVisibility(e.target.value)}><option value="PRIVATE">{s.visibilityPrivate}</option><option value="PROTECTED">{s.visibilityProtected}</option><option value="PUBLIC">{s.visibilityPublic}</option></select></label>
                <label>{s.workersLabel}<input data-testid="workers" type="number" min="1" max="16" value={config.worker_count} onChange={(e) => setConfig({ ...config, worker_count: Number(e.target.value || 1) })} /></label>
              </div>
            </div>
            <div className="actions two">
              <button data-testid="save-config" className="secondary" onClick={saveConfig}>{s.saveBtn}</button>
              <button data-testid="verify-config" onClick={verifyConfig}>{verifying ? s.verifying : s.verifyBtn}</button>
            </div>
            {showSavedToast && <div className="saved">{s.savedToast}</div>}
          </section>

          <section className="panel">
            <h2>{s.statusTitle}</h2>
            <StatusRow title="memos" status={memosStatus} />
            <StatusRow title="Notion" status={notionStatus} />
          </section>

          <section className="panel jobs-panel">
            <h2>{s.jobsTitle}</h2>
            {jobs.length === 0 && <div className="empty-small">{s.jobsEmpty}</div>}
            <div className="job-list">
              {jobs.map((job) => {
                const summary = job.summary || summarizeItems(jobDetail?.job.id === job.id ? jobDetail.items : []);
                return (
                  <div className="job-card" key={job.id} onClick={() => loadJob(job.id).catch(handleError)}>
                    <div className="job-head">
                      <span className="job-name">{job.source} · {summary.total || 0}</span>
                      <span className={`badge ${statusClass(job.status)}`}>{statusLabel(job.status, s)}</span>
                    </div>
                    <div className="progress"><span style={{ width: `${summary.progress_percent || 0}%` }} /></div>
                    <div className="job-meta"><span>{shortTime(job.created_at)}</span><span>{summary.completed}/{summary.total}</span></div>
                    <div className="job-actions">
                      <button data-testid={`open-${job.id}`} className="mini secondary" onClick={(e) => { e.stopPropagation(); loadJob(job.id).catch(handleError); }}>{s.openBtn}</button>
                      {job.status === "running" && <button data-testid={`cancel-${job.id}`} className="mini secondary" onClick={(e) => { e.stopPropagation(); cancelJob(job.id); }}>{s.cancelBtn}</button>}
                      {(job.status === "failed" || job.status === "canceled") && <button data-testid={`resume-${job.id}`} className="mini secondary" onClick={(e) => { e.stopPropagation(); resumeJob(job.id); }}>{s.resumeBtn}</button>}
                      {job.status === "failed" && <button data-testid={`retry-${job.id}`} className="mini secondary" onClick={(e) => { e.stopPropagation(); retryJob(job.id); }}>{s.retryBtn}</button>}
                    </div>
                  </div>
                );
              })}
            </div>
          </section>
        </aside>

        <section className="main-column">
          <div className="toolbar">
            <h2>{s.docsTitle}</h2>
            <input data-testid="document-search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={s.searchPlaceholder} />
            <button data-testid="load-documents" className="secondary" onClick={loadDocuments}>{docsLoadState === "loading" ? s.loadingBtn : s.loadBtn}</button>
            <span className="selection-count">{totalPages ? `${s.selected} ${selectedPages} / ${totalPages}` : ""}</span>
            <button data-testid="start-import" className={importing ? "loading-button" : ""} aria-busy={importing} disabled={importDisabled} onClick={startImport}>{importing || anyRunning ? s.importingBtn : s.importBtn}</button>
          </div>

          <div className="content-grid">
            <div className="doc-list">
              {docsLoadState === "idle" && <EmptyState title={s.listEmptyTitle} desc={s.listEmptyDesc} icon="☰" />}
              {docsLoadState === "loading" && <SkeletonRows />}
              {docsLoadState === "loaded" && visibleDocuments.map((doc) => (
                <div key={doc.id} className={`doc-row ${previewID === doc.id ? "selected" : ""}`} style={{ paddingLeft: `${10 + Math.min(doc.depth, 4) * 20}px` }} onClick={() => loadPreview(doc.id)}>
                  <input data-testid={`doc-${doc.id}`} type="checkbox" checked={!!selected[doc.id]} onClick={(e) => e.stopPropagation()} onChange={(e) => toggleDocument(doc, e.target.checked)} />
                  <span className="doc-title" title={doc.title || doc.id}>{doc.title || doc.id}</span>
                  <span className="doc-kind">{doc.kind === "database" ? s.docTypeDatabase : s.docTypePage}</span>
                </div>
              ))}
            </div>

            <div className="preview-panel">
              {!selectedPreviewDoc && <EmptyState title={s.previewEmptyTitle} desc={s.previewEmptyDesc} icon="◧" />}
              {selectedPreviewDoc?.kind === "database" && (
                <div className="preview-body"><h2>{selectedPreviewDoc.title}</h2><span className="doc-kind">{s.noPreviewForDatabase}</span></div>
              )}
              {previewLoading && <SkeletonRows />}
              {preview && (
                <div className="preview-body">
                  <div className="preview-head">
                    <h2>{preview.document.title || preview.document.id}</h2>
                    {preview.over_limit && <span className="badge error">{s.overLimit}</span>}
                  </div>
                  {preview.warnings && preview.warnings.length > 0 && (
                    <div className="warning-box">
                      <strong>{s.warningsLabel}</strong>
                      {preview.warnings.map((warning, index) => <div key={index}>{formatWarning(warning)}</div>)}
                    </div>
                  )}
                  <pre data-testid="preview-output" className="markdown-preview">{preview.markdown}</pre>
                  <div className="preview-meta">
                    <span>{preview.content_length || 0} bytes</span>
                    <span>{preview.attachment_count || 0} attachments</span>
                  </div>
                </div>
              )}
            </div>
          </div>

          {jobDetail && (
            <section className="detail-panel">
              <div className="detail-head">
                <h2>{s.resultTitle}</h2>
                <span className={`badge ${statusClass(jobDetail.job.status)}`}>{statusLabel(jobDetail.job.status, s)}</span>
              </div>
              <div data-testid="job-detail" className="item-list">
                {jobDetail.items.map((item) => {
                  const warnings = parseWarnings(item.warnings);
                  const warningText = warnings.map(formatWarning).join(" | ");
                  return (
                    <div className="item-row" key={item.external_id}>
                      <span className="item-title">{item.title || item.external_id}</span>
                      <span className={`badge ${statusClass(item.status)}`}>{item.status}</span>
                      <span>{item.error_stage || ""}</span>
                      <span>{item.error || ""}</span>
                      <span className="warning-text" title={warningText} tabIndex={warningText ? 0 : undefined}>{warningText}</span>
                    </div>
                  );
                })}
              </div>
            </section>
          )}
        </section>
      </main>
    </div>
  );
}

function StatusRow({ title, status }: { title: string; status: { tone: StatusTone; label: string; detail: string } }) {
  const icon = status.tone === "ok" ? "✓" : status.tone === "error" ? "×" : status.tone === "checking" ? "…" : "-";
  return (
    <div className={`status-row ${status.tone}`}>
      <span className="status-icon">{icon}</span>
      <span><strong>{title} · {status.label}</strong><small>{status.detail}</small></span>
    </div>
  );
}

function EmptyState({ title, desc, icon }: { title: string; desc: string; icon: string }) {
  return <div className="empty-state"><span>{icon}</span><strong>{title}</strong><p>{desc}</p></div>;
}

function SkeletonRows() {
  return <div className="skeleton">{[70, 52, 82, 64, 44].map((width) => <span key={width} style={{ width: `${width}%` }} />)}</div>;
}

createRoot(document.getElementById("root")!).render(<App />);
