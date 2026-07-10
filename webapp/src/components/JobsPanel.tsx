import { shortTime, statusClass, statusLabel, summarizeItems } from "../job-presentation";
import type { Strings } from "../strings";
import type { Job, JobDetail } from "../types";

type JobsPanelProps = {
  strings: Strings;
  jobs: Job[];
  jobDetail: JobDetail | null;
  onOpenJob: (id: string) => void;
  onCancelJob: (id: string) => void;
  onResumeJob: (id: string) => void;
  onRetryJob: (id: string) => void;
};

export function JobsPanel({
  strings,
  jobs,
  jobDetail,
  onOpenJob,
  onCancelJob,
  onResumeJob,
  onRetryJob,
}: JobsPanelProps) {
  return (
    <section className="panel jobs-panel">
      <h2>{strings.jobsTitle}</h2>
      {jobs.length === 0 && <div className="empty-small">{strings.jobsEmpty}</div>}
      <div className="job-list">
        {jobs.map((job) => {
          const summary = job.summary || summarizeItems(jobDetail?.job.id === job.id ? jobDetail.items : []);
          return (
            <div className="job-card" key={job.id} onClick={() => onOpenJob(job.id)}>
              <div className="job-head">
                <span className="job-name">{job.source} · {summary.total || 0}</span>
                <span className={`badge ${statusClass(job.status)}`}>{statusLabel(job.status, strings)}</span>
              </div>
              <div className="progress"><span style={{ width: `${summary.progress_percent || 0}%` }} /></div>
              <div className="job-meta"><span>{shortTime(job.created_at)}</span><span>{summary.completed}/{summary.total}</span></div>
              <div className="job-actions">
                <button data-testid={`open-${job.id}`} className="mini secondary" onClick={(e) => { e.stopPropagation(); onOpenJob(job.id); }}>{strings.openBtn}</button>
                {job.status === "running" && <button data-testid={`cancel-${job.id}`} className="mini secondary" onClick={(e) => { e.stopPropagation(); onCancelJob(job.id); }}>{strings.cancelBtn}</button>}
                {(job.status === "failed" || job.status === "canceled") && <button data-testid={`resume-${job.id}`} className="mini secondary" onClick={(e) => { e.stopPropagation(); onResumeJob(job.id); }}>{strings.resumeBtn}</button>}
                {job.status === "failed" && <button data-testid={`retry-${job.id}`} className="mini secondary" onClick={(e) => { e.stopPropagation(); onRetryJob(job.id); }}>{strings.retryBtn}</button>}
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}
