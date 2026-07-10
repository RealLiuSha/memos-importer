import type { Warning } from "./types";

export function parseWarnings(raw: string | undefined): Warning[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export function formatWarning(warning: Warning): string {
  return [warning.code, warning.message].filter(Boolean).join(": ") || "warning";
}
