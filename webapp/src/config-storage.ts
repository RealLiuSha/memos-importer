import type { BrowserConfig } from "./types";

const CONFIG_STORAGE_KEY = "memos-importer.config.v1";

const defaultBrowserConfig: BrowserConfig = {
  memos_endpoint: "",
  memos_token: "",
  notion_token: "",
  notion_time_source: "created_time",
  worker_count: 4,
};

export function readBrowserConfig(): BrowserConfig {
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

export function writeBrowserConfig(value: BrowserConfig): void {
  window.localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(value));
}
