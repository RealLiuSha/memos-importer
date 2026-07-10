export type Lang = "zh" | "en";
export type StatusTone = "idle" | "checking" | "ok" | "error";
export type TimeSourceMode = "created_time" | "last_edited_time" | "property";

export type ConfigDraft = {
  memos_endpoint: string;
  memos_token: string;
  notion_token: string;
  worker_count: number;
};

export type ConfigResponse = {
  memos_endpoint?: string;
  memos_token?: string;
  notion_token?: string;
  notion_time_source?: string;
  worker_count?: number;
};

export type BrowserConfig = {
  memos_endpoint: string;
  memos_token: string;
  notion_token: string;
  notion_time_source: string;
  worker_count: number;
};

export type ConfigPayload = {
  memos_endpoint: string;
  notion_time_source: string;
  worker_count: number;
  memos_token?: string;
  notion_token?: string;
};

export type VerifyResponse = {
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

export type DocumentRef = {
  source: string;
  id: string;
  title: string;
  updated_at: string;
  parent_id?: string;
  kind?: string;
};

export type Warning = {
  code?: string;
  message?: string;
  block_id?: string;
  severity?: string;
};

export type PreviewResponse = {
  document: DocumentRef;
  markdown: string;
  warnings?: Warning[];
  attachment_count?: number;
  content_length?: number;
  content_length_limit?: number;
  over_limit?: boolean;
};

export type JobSummary = {
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

export type Job = {
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

export type ImportItem = {
  external_id: string;
  title: string;
  status: string;
  warnings?: string;
  error_stage?: string;
  error?: string;
  memo_id?: string;
};

export type JobDetail = {
  job: Job;
  items: ImportItem[];
  summary?: JobSummary;
};

export type DisplayDocument = DocumentRef & { depth: number };

export type ImportOptions = {
  strategy: string;
  visibility: string;
  worker_count: number;
  time_source: string;
};
