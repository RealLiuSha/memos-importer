import React, { useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";

import {
  APIError,
  cancelImportJob,
  createImportJob,
  createSession,
  fetchConfig,
  fetchJob,
  fetchJobs,
  fetchNotionDocuments,
  fetchNotionPreview,
  openJobEvents,
  resumeImportJob,
  retryImportJob,
  verifyImporterConfig,
} from "./api-client";
import { ConfigurationPanel } from "./components/ConfigurationPanel";
import { DocumentsPanel } from "./components/DocumentsPanel";
import { JobDetailPanel } from "./components/JobDetailPanel";
import { JobsPanel } from "./components/JobsPanel";
import { readBrowserConfig, writeBrowserConfig } from "./config-storage";
import { STRINGS } from "./strings";
import type {
  ConfigDraft,
  ConfigPayload,
  DisplayDocument,
  DocumentRef,
  Job,
  JobDetail,
  Lang,
  PreviewResponse,
  StatusTone,
  TimeSourceMode,
  VerifyResponse,
} from "./types";
import "./styles.css";

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

function decorateDocuments(docs: DocumentRef[]): DisplayDocument[] {
  const byID = new Map(docs.map((doc) => [doc.id, doc]));
  return docs.map((doc) => {
    let depth = 0;
    let parentID = doc.parent_id;
    const seen = new Set([doc.id]);
    while (parentID && byID.has(parentID) && !seen.has(parentID)) {
      seen.add(parentID);
      depth += 1;
      parentID = byID.get(parentID)?.parent_id;
    }
    return { ...doc, depth };
  });
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
  const [documentLimit, setDocumentLimit] = useState("100");
  const [documentsHasMore, setDocumentsHasMore] = useState(false);
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
    const decorated = decorateDocuments(documents);
    return normalized
      ? decorated.filter((doc) => [doc.title, doc.id, doc.kind, doc.parent_id]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(normalized)))
      : decorated;
  }, [documents, search]);

  const selectedIDs = useMemo(() => Object.keys(selected).filter((id) => selected[id]), [selected]);
  const totalPages = documents.filter((doc) => doc.kind !== "database").length;
  const selectedPages = documents.filter((doc) => doc.kind !== "database" && selected[doc.id]).length;
  const anyRunning = jobs.some((job) => job.status === "running");
  const selectedPreviewDoc = previewID ? documents.find((doc) => doc.id === previewID) : null;

  async function loadConfig() {
    const cfg = await fetchConfig();
    setConfig((old) => ({
      ...old,
      memos_endpoint: old.memos_endpoint || cfg.memos_endpoint || "",
      worker_count: Number(old.worker_count || cfg.worker_count || 4),
    }));
    setMaskedTokens({ memos: cfg.memos_token || "", notion: cfg.notion_token || "" });
  }

  function configPayload() {
    const payload: ConfigPayload = {
      memos_endpoint: config.memos_endpoint,
      notion_time_source: buildTimeSource(timeSourceMode, timeSourceProperty),
      worker_count: config.worker_count,
    };
    if (config.memos_token.trim()) payload.memos_token = config.memos_token.trim();
    if (config.notion_token.trim()) payload.notion_token = config.notion_token.trim();
    return payload;
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
      const data = await verifyImporterConfig(configPayload());
      setVerifyResult(data);
      setMessage("");
    } catch (err) {
      handleError(err);
    } finally {
      setVerifying(false);
    }
  }

  async function loadDocuments() {
    const limit = Number(documentLimit);
    if (!Number.isInteger(limit) || limit < 1 || limit > 1000) {
      setMessage(s.invalidDocumentLimit);
      return;
    }
    setDocsLoadState("loading");
    try {
      const data = await fetchNotionDocuments(configPayload(), limit);
      const docs = data.documents || [];
      setDocuments(docs);
      setDocumentsHasMore(!!data.has_more);
      setDocsLoadState("loaded");
      setSelected({});
      setPreview(null);
      setPreviewID(docs[0]?.id || null);
      if (docs[0] && docs[0].kind !== "database") await loadPreview(docs[0].id);
      setMessage("");
    } catch (err) {
      setDocsLoadState("idle");
      setDocumentsHasMore(false);
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
      const data = await fetchNotionPreview(id, configPayload());
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
      const titleByID = Object.fromEntries(
        documents.filter((doc) => selected[doc.id]).map((doc) => [doc.id, doc.title || doc.id]),
      );
      const data = await createImportJob(selectedIDs, titleByID, configPayload(), {
        strategy,
        visibility,
        worker_count: config.worker_count,
        time_source: buildTimeSource(timeSourceMode, timeSourceProperty),
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
    const events = openJobEvents(jobID);
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
    const data = await fetchJobs();
    setJobs(data.jobs || []);
  }

  async function loadJob(id: string) {
    const data = await fetchJob(id);
    setJobDetail(data);
  }

  function openJob(id: string) {
    loadJob(id).catch(handleError);
  }

  async function cancelJob(id: string) {
    try {
      await cancelImportJob(id);
      await loadJob(id);
      await loadJobs();
    } catch (err) {
      handleError(err);
    }
  }

  async function retryJob(id: string) {
    try {
      await retryImportJob(id, configPayload());
      subscribeJob(id);
      await loadJob(id);
      await loadJobs();
    } catch (err) {
      handleError(err);
    }
  }

  async function resumeJob(id: string) {
    try {
      await resumeImportJob(id, configPayload());
      subscribeJob(id);
      await loadJob(id);
      await loadJobs();
    } catch (err) {
      handleError(err);
    }
  }

  async function unlock() {
    try {
      await createSession(accessPassword);
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
          <ConfigurationPanel
            strings={s}
            config={config}
            maskedTokens={maskedTokens}
            timeSourceMode={timeSourceMode}
            timeSourceProperty={timeSourceProperty}
            strategy={strategy}
            visibility={visibility}
            verifying={verifying}
            showSavedToast={showSavedToast}
            memosStatus={memosStatus}
            notionStatus={notionStatus}
            onConfigChange={setConfig}
            onTimeSourceModeChange={setTimeSourceMode}
            onTimeSourcePropertyChange={setTimeSourceProperty}
            onStrategyChange={setStrategy}
            onVisibilityChange={setVisibility}
            onSave={saveConfig}
            onVerify={verifyConfig}
          />
          <JobsPanel
            strings={s}
            jobs={jobs}
            jobDetail={jobDetail}
            onOpenJob={openJob}
            onCancelJob={cancelJob}
            onResumeJob={resumeJob}
            onRetryJob={retryJob}
          />
        </aside>

        <section className="main-column">
          <DocumentsPanel
            strings={s}
            docsLoadState={docsLoadState}
            documentLimit={documentLimit}
            documentsHasMore={documentsHasMore}
            search={search}
            visibleDocuments={visibleDocuments}
            selected={selected}
            previewID={previewID}
            selectedPreviewDoc={selectedPreviewDoc}
            preview={preview}
            previewLoading={previewLoading}
            totalPages={totalPages}
            selectedPages={selectedPages}
            importing={importing}
            anyRunning={anyRunning}
            importDisabled={importDisabled}
            onSearchChange={setSearch}
            onDocumentLimitChange={setDocumentLimit}
            onLoadDocuments={loadDocuments}
            onStartImport={startImport}
            onLoadPreview={loadPreview}
            onToggleDocument={toggleDocument}
          />
          {jobDetail && <JobDetailPanel strings={s} jobDetail={jobDetail} />}
        </section>
      </main>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<App />);
