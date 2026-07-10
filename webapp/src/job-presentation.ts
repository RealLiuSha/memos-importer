import type { Strings } from "./strings";
import type { ImportItem, JobSummary } from "./types";

export function summarizeItems(items: ImportItem[]): JobSummary {
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

export function statusLabel(status: string, strings: Strings): string {
  if (status === "done") return strings.jobDone;
  if (status === "failed") return strings.jobFailed;
  if (status === "running") return strings.jobRunning;
  if (status === "canceled") return strings.jobCanceled;
  return strings.jobPending;
}

export function statusClass(status: string): string {
  if (status === "done") return "ok";
  if (status === "failed") return "error";
  if (status === "running") return "running";
  if (status === "canceled") return "muted";
  return "pending";
}

export function shortTime(value: string | undefined): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return `${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")} ${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
}
