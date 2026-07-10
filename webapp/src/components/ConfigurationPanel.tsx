import type { Strings } from "../strings";
import type { ConfigDraft, StatusTone, TimeSourceMode } from "../types";

type ConnectionStatus = {
  tone: StatusTone;
  label: string;
  detail: string;
};

type ConfigurationPanelProps = {
  strings: Strings;
  config: ConfigDraft;
  maskedTokens: { memos: string; notion: string };
  timeSourceMode: TimeSourceMode;
  timeSourceProperty: string;
  strategy: string;
  visibility: string;
  verifying: boolean;
  showSavedToast: boolean;
  memosStatus: ConnectionStatus;
  notionStatus: ConnectionStatus;
  onConfigChange: (config: ConfigDraft) => void;
  onTimeSourceModeChange: (mode: TimeSourceMode) => void;
  onTimeSourcePropertyChange: (property: string) => void;
  onStrategyChange: (strategy: string) => void;
  onVisibilityChange: (visibility: string) => void;
  onSave: () => void;
  onVerify: () => void;
};

export function ConfigurationPanel({
  strings,
  config,
  maskedTokens,
  timeSourceMode,
  timeSourceProperty,
  strategy,
  visibility,
  verifying,
  showSavedToast,
  memosStatus,
  notionStatus,
  onConfigChange,
  onTimeSourceModeChange,
  onTimeSourcePropertyChange,
  onStrategyChange,
  onVisibilityChange,
  onSave,
  onVerify,
}: ConfigurationPanelProps) {
  return (
    <>
      <section className="panel">
        <h2>{strings.configTitle}</h2>
        <div className="field-stack">
          <label>{strings.endpointLabel}<input data-testid="memos-endpoint" value={config.memos_endpoint} onChange={(e) => onConfigChange({ ...config, memos_endpoint: e.target.value })} /></label>
          <label>{strings.memosTokenLabel}<input data-testid="memos-token" type="password" value={config.memos_token} placeholder={maskedTokens.memos ? strings.maskedToken : ""} onChange={(e) => onConfigChange({ ...config, memos_token: e.target.value })} /></label>
          <label>{strings.notionTokenLabel}<input data-testid="notion-token" type="password" value={config.notion_token} placeholder={maskedTokens.notion ? strings.maskedToken : ""} onChange={(e) => onConfigChange({ ...config, notion_token: e.target.value })} /></label>
          <label>{strings.timeSourceLabel}
            <select data-testid="time-source" value={timeSourceMode} onChange={(e) => onTimeSourceModeChange(e.target.value as TimeSourceMode)}>
              <option value="created_time">created_time</option>
              <option value="last_edited_time">last_edited_time</option>
              <option value="property">property</option>
            </select>
          </label>
          {timeSourceMode === "property" && (
            <label>{strings.propertyLabel}<input data-testid="time-source-property" value={timeSourceProperty} onChange={(e) => onTimeSourcePropertyChange(e.target.value)} /></label>
          )}
          <div className="option-grid">
            <label>{strings.strategyLabel}<select data-testid="strategy" value={strategy} onChange={(e) => onStrategyChange(e.target.value)}><option value="skip">skip</option><option value="overwrite">overwrite</option></select></label>
            <label>{strings.visibilityLabel}<select data-testid="visibility" value={visibility} onChange={(e) => onVisibilityChange(e.target.value)}><option value="PRIVATE">{strings.visibilityPrivate}</option><option value="PROTECTED">{strings.visibilityProtected}</option><option value="PUBLIC">{strings.visibilityPublic}</option></select></label>
            <label>{strings.workersLabel}<input data-testid="workers" type="number" min="1" max="16" value={config.worker_count} onChange={(e) => onConfigChange({ ...config, worker_count: Number(e.target.value || 1) })} /></label>
          </div>
        </div>
        <div className="actions two">
          <button data-testid="save-config" className="secondary" onClick={onSave}>{strings.saveBtn}</button>
          <button data-testid="verify-config" onClick={onVerify}>{verifying ? strings.verifying : strings.verifyBtn}</button>
        </div>
        {showSavedToast && <div className="saved">{strings.savedToast}</div>}
      </section>

      <section className="panel">
        <h2>{strings.statusTitle}</h2>
        <StatusRow title="memos" status={memosStatus} />
        <StatusRow title="Notion" status={notionStatus} />
      </section>
    </>
  );
}

function StatusRow({ title, status }: { title: string; status: ConnectionStatus }) {
  const icon = status.tone === "ok" ? "✓" : status.tone === "error" ? "×" : status.tone === "checking" ? "…" : "-";
  return (
    <div className={`status-row ${status.tone}`}>
      <span className="status-icon">{icon}</span>
      <span><strong>{title} · {status.label}</strong><small>{status.detail}</small></span>
    </div>
  );
}
