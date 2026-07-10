import { statusClass, statusLabel } from "../job-presentation";
import type { Strings } from "../strings";
import type { JobDetail } from "../types";
import { formatWarning, parseWarnings } from "../warnings";

type JobDetailPanelProps = {
  strings: Strings;
  jobDetail: JobDetail;
};

export function JobDetailPanel({ strings, jobDetail }: JobDetailPanelProps) {
  return (
    <section className="detail-panel">
      <div className="detail-head">
        <h2>{strings.resultTitle}</h2>
        <span className={`badge ${statusClass(jobDetail.job.status)}`}>{statusLabel(jobDetail.job.status, strings)}</span>
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
  );
}
