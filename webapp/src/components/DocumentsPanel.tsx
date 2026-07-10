import { formatWarning } from "../warnings";
import type { Strings } from "../strings";
import type { DisplayDocument, DocumentRef, PreviewResponse } from "../types";

type DocumentsPanelProps = {
  strings: Strings;
  docsLoadState: "idle" | "loading" | "loaded";
  search: string;
  visibleDocuments: DisplayDocument[];
  selected: Record<string, boolean>;
  previewID: string | null;
  selectedPreviewDoc: DocumentRef | null | undefined;
  preview: PreviewResponse | null;
  previewLoading: boolean;
  totalPages: number;
  selectedPages: number;
  importing: boolean;
  anyRunning: boolean;
  importDisabled: boolean;
  onSearchChange: (search: string) => void;
  onLoadDocuments: () => void;
  onStartImport: () => void;
  onLoadPreview: (id: string) => void;
  onToggleDocument: (doc: DocumentRef, checked: boolean) => void;
};

export function DocumentsPanel({
  strings,
  docsLoadState,
  search,
  visibleDocuments,
  selected,
  previewID,
  selectedPreviewDoc,
  preview,
  previewLoading,
  totalPages,
  selectedPages,
  importing,
  anyRunning,
  importDisabled,
  onSearchChange,
  onLoadDocuments,
  onStartImport,
  onLoadPreview,
  onToggleDocument,
}: DocumentsPanelProps) {
  return (
    <>
      <div className="toolbar">
        <h2>{strings.docsTitle}</h2>
        <input data-testid="document-search" value={search} onChange={(e) => onSearchChange(e.target.value)} placeholder={strings.searchPlaceholder} />
        <button data-testid="load-documents" className="secondary" onClick={onLoadDocuments}>{docsLoadState === "loading" ? strings.loadingBtn : strings.loadBtn}</button>
        <span className="selection-count">{totalPages ? `${strings.selected} ${selectedPages} / ${totalPages}` : ""}</span>
        <button data-testid="start-import" className={importing ? "loading-button" : ""} aria-busy={importing} disabled={importDisabled} onClick={onStartImport}>{importing || anyRunning ? strings.importingBtn : strings.importBtn}</button>
      </div>

      <div className="content-grid">
        <div className="doc-list">
          {docsLoadState === "idle" && <EmptyState title={strings.listEmptyTitle} desc={strings.listEmptyDesc} icon="☰" />}
          {docsLoadState === "loading" && <SkeletonRows />}
          {docsLoadState === "loaded" && visibleDocuments.map((doc) => (
            <div key={doc.id} className={`doc-row ${previewID === doc.id ? "selected" : ""}`} style={{ paddingLeft: `${10 + Math.min(doc.depth, 4) * 20}px` }} onClick={() => onLoadPreview(doc.id)}>
              <input data-testid={`doc-${doc.id}`} type="checkbox" checked={!!selected[doc.id]} onClick={(e) => e.stopPropagation()} onChange={(e) => onToggleDocument(doc, e.target.checked)} />
              <span className="doc-title" title={doc.title || doc.id}>{doc.title || doc.id}</span>
              <span className="doc-kind">{doc.kind === "database" ? strings.docTypeDatabase : strings.docTypePage}</span>
            </div>
          ))}
        </div>

        <div className="preview-panel">
          {!selectedPreviewDoc && <EmptyState title={strings.previewEmptyTitle} desc={strings.previewEmptyDesc} icon="◧" />}
          {selectedPreviewDoc?.kind === "database" && (
            <div className="preview-body"><h2>{selectedPreviewDoc.title}</h2><span className="doc-kind">{strings.noPreviewForDatabase}</span></div>
          )}
          {previewLoading && <SkeletonRows />}
          {preview && (
            <div className="preview-body">
              <div className="preview-head">
                <h2>{preview.document.title || preview.document.id}</h2>
                {preview.over_limit && <span className="badge error">{strings.overLimit}</span>}
              </div>
              {preview.warnings && preview.warnings.length > 0 && (
                <div className="warning-box">
                  <strong>{strings.warningsLabel}</strong>
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
    </>
  );
}

function EmptyState({ title, desc, icon }: { title: string; desc: string; icon: string }) {
  return <div className="empty-state"><span>{icon}</span><strong>{title}</strong><p>{desc}</p></div>;
}

function SkeletonRows() {
  return <div className="skeleton">{[70, 52, 82, 64, 44].map((width) => <span key={width} style={{ width: `${width}%` }} />)}</div>;
}
