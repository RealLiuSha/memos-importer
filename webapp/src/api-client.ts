import type {
  ConfigPayload,
  ConfigResponse,
  DocumentRef,
  ImportOptions,
  Job,
  JobDetail,
  PreviewResponse,
  VerifyResponse,
} from "./types";

export class APIError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new APIError(body.error || response.statusText, response.status);
  return body;
}

export function fetchConfig(): Promise<ConfigResponse> {
  return request("/api/config");
}

export function verifyImporterConfig(config: ConfigPayload): Promise<VerifyResponse> {
  return request("/api/config/verify", {
    method: "POST",
    body: JSON.stringify(config),
  });
}

export function fetchNotionDocuments(config: ConfigPayload, limit: number): Promise<{ documents: DocumentRef[]; has_more?: boolean }> {
  return request(`/api/sources/notion/tree?limit=${encodeURIComponent(limit)}`, {
    method: "POST",
    body: JSON.stringify({ config }),
  });
}

export function fetchNotionPreview(id: string, config: ConfigPayload): Promise<PreviewResponse> {
  return request(`/api/sources/notion/documents/${encodeURIComponent(id)}/preview`, {
    method: "POST",
    body: JSON.stringify({ config }),
  });
}

export function createImportJob(
  externalIDs: string[],
  titleByID: Record<string, string>,
  config: ConfigPayload,
  options: ImportOptions,
): Promise<{ job_id: string }> {
  return request("/api/jobs", {
    method: "POST",
    body: JSON.stringify({
      source: "notion",
      external_ids: externalIDs,
      title_by_id: titleByID,
      config,
      options,
    }),
  });
}

export function openJobEvents(jobID: string): EventSource {
  return new EventSource(`/api/jobs/${encodeURIComponent(jobID)}/events`, {
    withCredentials: true,
  });
}

export function fetchJobs(): Promise<{ jobs: Job[] }> {
  return request("/api/jobs");
}

export function fetchJob(id: string): Promise<JobDetail> {
  return request(`/api/jobs/${encodeURIComponent(id)}`);
}

export function cancelImportJob(id: string): Promise<unknown> {
  return request(`/api/jobs/${encodeURIComponent(id)}/cancel`, { method: "POST" });
}

export function retryImportJob(id: string, config: ConfigPayload): Promise<unknown> {
  return request(`/api/jobs/${encodeURIComponent(id)}/retry`, {
    method: "POST",
    body: JSON.stringify({ config }),
  });
}

export function resumeImportJob(id: string, config: ConfigPayload): Promise<unknown> {
  return request(`/api/jobs/${encodeURIComponent(id)}/resume`, {
    method: "POST",
    body: JSON.stringify({ config }),
  });
}

export function createSession(password: string): Promise<{ authenticated: boolean }> {
  return request("/api/session", {
    method: "POST",
    body: JSON.stringify({ password }),
  });
}
