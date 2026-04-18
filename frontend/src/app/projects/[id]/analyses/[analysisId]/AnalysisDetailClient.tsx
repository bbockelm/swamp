"use client";

import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type AnalysisResult } from "@/lib/api";
import { useRouter } from "next/navigation";
import { AnalysisStatus } from "@/components/AnalysisStatus";
import { SARIFViewer } from "@/components/SARIFViewer";
import { MarkdownReport, RenderedMarkdown } from "@/components/MarkdownReport";
import { useEffect, useRef, useState } from "react";
import { useResolvedParams } from "@/lib/useResolvedParams";
import { StreamLine, processLogLines } from "@/lib/stream-utils";

function timeAgo(dateStr: string): string {
  const seconds = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
  if (seconds < 60) return 'just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(dateStr).toLocaleDateString();
}

export default function AnalysisDetailClient() {
  const { id: projectId, analysisId } = useResolvedParams<{
    id: string;
    analysisId: string;
  }>('/projects/[id]/analyses/[analysisId]');
  const queryClient = useQueryClient();
  const router = useRouter();

  const { data: project } = useQuery({
    queryKey: ["project", projectId],
    queryFn: () => api.projects.get(projectId),
  });

  const canEdit = project?.my_role === 'write' || project?.my_role === 'admin';

  const { data: analysis, isLoading } = useQuery({
    queryKey: ["analysis", projectId, analysisId],
    queryFn: () => api.analyses.get(projectId, analysisId),
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "pending" || status === "running" ? 3000 : false;
    },
  });

  const isActive =
    analysis?.status === "pending" || analysis?.status === "running";

  const isTerminal =
    analysis?.status === "completed" ||
    analysis?.status === "failed" ||
    analysis?.status === "cancelled" ||
    analysis?.status === "timed_out";

  // Poll executor liveness for active jobs to detect stale states.
  const { data: liveness } = useQuery({
    queryKey: ["analysis-alive", projectId, analysisId],
    queryFn: () => api.analyses.checkAlive(projectId, analysisId),
    enabled: isActive,
    refetchInterval: isActive ? 10000 : false,
  });

  // If DB says running but executor says not alive, force a refetch of the
  // analysis status so the UI picks up the corrected state promptly.
  useEffect(() => {
    if (isActive && liveness && !liveness.alive) {
      const timer = setTimeout(() => {
        queryClient.invalidateQueries({
          queryKey: ["analysis", projectId, analysisId],
        });
      }, 2000);
      return () => clearTimeout(timer);
    }
  }, [isActive, liveness, projectId, analysisId, queryClient]);

  const { data: results } = useQuery({
    queryKey: ["results", projectId, analysisId],
    queryFn: () => api.analyses.listResults(projectId, analysisId),
    enabled: isTerminal,
  });

  const cancelMutation = useMutation({
    mutationFn: () => api.analyses.cancel(projectId, analysisId),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["analysis", projectId, analysisId],
      });
    },
  });

  const resubmitMutation = useMutation({
    mutationFn: () => api.analyses.resubmit(projectId, analysisId),
    onSuccess: (newAnalysis) => {
      queryClient.invalidateQueries({ queryKey: ["analyses"] });
      router.push(`/projects/${projectId}/analyses/${newAnalysis.id}`);
    },
  });

  if (isLoading) return <p>Loading...</p>;
  if (!analysis) return <p>Analysis not found.</p>;

  const canResubmit =
    analysis.status === "completed" ||
    analysis.status === "failed" ||
    analysis.status === "cancelled" ||
    analysis.status === "timed_out";

  const sarifResult = results?.find((r) => r.result_type === "sarif");
  const markdownResult = results?.find(
    (r) => r.result_type === "markdown" || r.result_type === "markdown_report",
  );
  const logResults =
    results?.filter((r) => r.result_type === "agent_log") ?? [];
  const notesResult = results?.find((r) => r.result_type === "analysis_notes");
  const promptResult = results?.find((r) => r.result_type === "analysis_prompt");
  const contextResult = results?.find((r) => r.result_type === "analysis_context");

  const specialTypes = new Set([
    "sarif", "markdown", "markdown_report", "agent_log",
    "analysis_notes", "analysis_prompt", "analysis_context",
  ]);
  const otherArtifacts = results?.filter((r) => !specialTypes.has(r.result_type)) ?? [];

  return (
    <div>
      <div className="flex justify-between items-start mb-6">
        <div>
          <h1 className="text-2xl font-bold">
            Analysis{" "}
            <span className="font-mono text-gray-500">
              {analysisId.slice(0, 8)}
            </span>
          </h1>
          <div className="mt-1 flex items-center gap-3">
            <AnalysisStatus status={analysis.status} />
            {analysis.status_detail && (
              <span className="text-sm text-gray-500">
                {analysis.status_detail}
              </span>
            )}
            {(analysis.status === "pending" || analysis.status === "running") && (
              <span className="text-sm text-gray-500 inline-flex items-center gap-1.5">
                <svg className="w-4 h-4 animate-spin text-blue-500" viewBox="0 0 24 24" fill="none">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                <ElapsedTime since={analysis.started_at || analysis.created_at} />
              </span>
            )}
          </div>
        </div>
        <div className="flex gap-2 print:hidden">
          {canEdit && (analysis.status === "pending" || analysis.status === "running") && (
            <button
              onClick={() => cancelMutation.mutate()}
              disabled={cancelMutation.isPending}
              className="bg-red-600 text-white px-3 py-1.5 text-sm rounded hover:bg-red-700 disabled:opacity-50"
            >
              Cancel
            </button>
          )}
          {canEdit && canResubmit && (
            <button
              onClick={() => resubmitMutation.mutate()}
              disabled={resubmitMutation.isPending}
              className="bg-blue-600 text-white px-3 py-1.5 text-sm rounded hover:bg-blue-700 disabled:opacity-50"
            >
              {resubmitMutation.isPending ? "Resubmitting…" : "Resubmit"}
            </button>
          )}
        </div>
      </div>

      {/* Metadata */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 bg-gray-50 p-4 rounded border mb-6">
        <div>
          <p className="text-xs text-gray-500 uppercase">Created</p>
          <p className="text-sm">
            {new Date(analysis.created_at).toLocaleString()}
          </p>
          <p className="text-xs text-gray-400">{timeAgo(analysis.created_at)}</p>
        </div>
        {analysis.started_at && (
          <div>
            <p className="text-xs text-gray-500 uppercase">Started</p>
            <p className="text-sm">
              {new Date(analysis.started_at).toLocaleString()}
            </p>
          </div>
        )}
        {analysis.completed_at && (
          <div>
            <p className="text-xs text-gray-500 uppercase">Completed</p>
            <p className="text-sm">
              {new Date(analysis.completed_at).toLocaleString()}
            </p>
          </div>
        )}
        {analysis.triggered_by && (
          <div>
            <p className="text-xs text-gray-500 uppercase">Triggered By</p>
            <p className="text-sm">
              {analysis.trigger_event && analysis.trigger_event !== 'manual' ? (
                <span className="inline-flex items-center gap-1">
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-800">
                    {analysis.trigger_event === 'push' ? '⬆ push' :
                     analysis.trigger_event === 'pull_request' ? '⑂ PR' :
                     analysis.trigger_event === 'release' ? '🏷 release' :
                     analysis.trigger_event}
                  </span>
                  {analysis.triggered_by_name || analysis.triggered_by.replace('webhook:', '')}
                </span>
              ) : (
                analysis.triggered_by_name || analysis.triggered_by.slice(0, 8)
              )}
            </p>
          </div>
        )}
        {analysis.trigger_event === 'pull_request' && !!analysis.trigger_meta?.pr_url && (
          <div>
            <p className="text-xs text-gray-500 uppercase">Pull Request</p>
            <p className="text-sm">
              <a
                href={analysis.trigger_meta.pr_url as string}
                target="_blank"
                rel="noopener noreferrer"
                className="text-blue-600 hover:underline"
              >
                PR #{String(analysis.trigger_meta.pr_number)}
                {analysis.trigger_meta.head_ref ? ` (${String(analysis.trigger_meta.head_ref)})` : ''}
                {' '}↗
              </a>
            </p>
          </div>
        )}
        {analysis.trigger_event === 'release' && !!analysis.trigger_meta?.release_url && (
          <div>
            <p className="text-xs text-gray-500 uppercase">Release</p>
            <p className="text-sm">
              <a
                href={analysis.trigger_meta.release_url as string}
                target="_blank"
                rel="noopener noreferrer"
                className="text-blue-600 hover:underline"
              >
                {String(analysis.trigger_meta.tag || 'release')} ↗
              </a>
            </p>
          </div>
        )}
        {analysis.git_branch && (
          <div>
            <p className="text-xs text-gray-500 uppercase">Branch</p>
            <p className="text-sm font-mono">{analysis.git_branch}</p>
          </div>
        )}
        {analysis.git_commit && (
          <div>
            <p className="text-xs text-gray-500 uppercase">Commit</p>
            <p className="text-sm font-mono">
              {analysis.trigger_meta?.repo ? (
                <a
                  href={`https://github.com/${analysis.trigger_meta.repo}/commit/${analysis.git_commit}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-blue-600 hover:underline"
                >
                  {analysis.git_commit.slice(0, 12)} ↗
                </a>
              ) : (
                analysis.git_commit.slice(0, 12)
              )}
            </p>
          </div>
        )}
        {analysis.sarif_upload_url && (
          <div>
            <p className="text-xs text-gray-500 uppercase">GitHub Code Scanning</p>
            <p className="text-sm">
              <a
                href={analysis.sarif_upload_url}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 text-blue-600 hover:underline"
              >
                <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-green-100 text-green-800">
                  ✓ Uploaded
                </span>
                View alerts ↗
              </a>
            </p>
          </div>
        )}
        {analysis.error_message && (
          <div className="col-span-full">
            <p className="text-xs text-red-500 uppercase">Error</p>
            <p className="text-sm text-red-700 font-mono">
              {analysis.error_message}
            </p>
          </div>
        )}
      </div>

      {/* Results */}
      {results && results.filter((r) => r.result_type !== "agent_log").length > 0 ? (
        <div className="space-y-6 mb-6">
          <div className="flex justify-between items-center">
            <h2 className="text-lg font-semibold">Results</h2>
            {/* Quick navigation */}
            <div className="flex items-center gap-3 print:hidden">
              {markdownResult && sarifResult && (
                <>
                  <a href="#security-report" className="text-sm text-gray-500 hover:text-blue-600">
                    Report
                  </a>
                  <span className="text-gray-300">|</span>
                  <a href="#findings" className="text-sm text-gray-500 hover:text-blue-600">
                    Findings ({sarifResult.finding_count})
                  </a>
                  <span className="text-gray-300">|</span>
                </>
              )}
              {markdownResult && (
                <button
                  onClick={() => window.print()}
                  className="inline-flex items-center gap-1.5 text-sm text-gray-500 hover:text-blue-600"
                  title="Print or save as PDF"
                >
                  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M17 17h2a2 2 0 002-2v-4a2 2 0 00-2-2H5a2 2 0 00-2 2v4a2 2 0 002 2h2m2 4h6a2 2 0 002-2v-4a2 2 0 00-2-2H9a2 2 0 00-2 2v4a2 2 0 002 2zm8-12V5a2 2 0 00-2-2H9a2 2 0 00-2 2v4h10z" />
                  </svg>
                  Download as PDF
                </button>
              )}
            </div>
          </div>

          {/* Markdown report */}
          {markdownResult && (
            <div id="security-report">
              <div className="flex justify-between items-center mb-2">
                <h3 className="font-medium">Security Report</h3>
                <a
                  href={api.analyses.downloadResult(
                    projectId,
                    analysisId,
                    markdownResult.id,
                  )}
                  className="text-blue-600 text-sm hover:underline print:hidden"
                >
                  Download Markdown
                </a>
              </div>
              <MarkdownReport
                projectId={projectId}
                analysisId={analysisId}
                resultId={markdownResult.id}
              />
            </div>
          )}

          {/* SARIF findings */}
          {sarifResult && (
            <div id="findings">
              <div className="flex justify-between items-center mb-2">
                <h3 className="font-medium">
                  Findings ({sarifResult.finding_count})
                </h3>
                <div className="flex items-center gap-3 print:hidden">
                  {markdownResult && (
                    <a href="#security-report" className="text-gray-500 text-sm hover:text-blue-600">
                      ↑ Back to Report
                    </a>
                  )}
                  <a
                    href={api.analyses.downloadResult(
                      projectId,
                      analysisId,
                      sarifResult.id,
                    )}
                    className="text-blue-600 text-sm hover:underline"
                  >
                    Download SARIF
                  </a>
                </div>
              </div>
              <SARIFViewer
                projectId={projectId}
                analysisId={analysisId}
                resultId={sarifResult.id}
              />
            </div>
          )}

          {/* Other artifacts */}
          {otherArtifacts.length > 0 && (
            <div>
              <h3 className="font-medium mb-2">Other Artifacts</h3>
              <div className="space-y-2">
                {otherArtifacts.map((r) => (
                    <a
                      key={r.id}
                      href={api.analyses.downloadResult(
                        projectId,
                        analysisId,
                        r.id,
                      )}
                      className="block p-3 bg-white border rounded hover:bg-gray-50 text-sm"
                    >
                      <span className="font-medium">{r.filename}</span>
                      <span className="text-gray-400 ml-2">
                        ({(r.file_size / 1024).toFixed(1)} KB)
                      </span>
                    </a>
                  ))}
              </div>
            </div>
          )}

          {/* Analyst notes from this run */}
          {notesResult && (
            <CollapsibleResultSection
              title="Analyst Notes"
              subtitle="Notes captured by the agent for future runs"
              resultId={notesResult.id}
              projectId={projectId}
              analysisId={analysisId}
              defaultOpen={false}
            />
          )}

          {/* Prompt used */}
          {promptResult && (
            <CollapsibleResultSection
              title="Prompt"
              subtitle="Full prompt sent to the analysis agent"
              resultId={promptResult.id}
              projectId={projectId}
              analysisId={analysisId}
              defaultOpen={false}
            />
          )}

          {/* Context provided */}
          {contextResult && (
            <CollapsibleResultSection
              title="Context Provided"
              subtitle="Prior findings and notes injected from earlier runs"
              resultId={contextResult.id}
              projectId={projectId}
              analysisId={analysisId}
              defaultOpen={false}
            />
          )}
        </div>
      ) : isTerminal && (
        <p className="text-sm text-gray-500 mt-4 mb-6">No results were produced for this analysis.</p>
      )}

      {/* Output: live WS stream while active, archived logs after completion */}
      <div className="print:hidden">
      {!isTerminal ? (
        <TerminalStream
          analysisId={analysisId}
          analysisStatus={analysis.status}
        />
      ) : (
        logResults.length > 0 && (
          <ArchivedOutput
            logs={logResults}
            projectId={projectId}
            analysisId={analysisId}
          />
        )
      )}
      </div>
    </div>
  );
}

/** Collapsible section that fetches and renders a markdown result artifact inline. */
function CollapsibleResultSection({
  title,
  subtitle,
  resultId,
  projectId,
  analysisId,
  defaultOpen,
}: {
  title: string;
  subtitle: string;
  resultId: string;
  projectId: string;
  analysisId: string;
  defaultOpen: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState("");
  const printRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open || content !== null) return;
    const url = api.analyses.downloadResult(projectId, analysisId, resultId);
    fetch(url, { credentials: "include" })
      .then((r) => {
        if (!r.ok) throw new Error("Failed to load");
        return r.text();
      })
      .then((text) => setContent(text))
      .catch((err) => setError(err.message));
  }, [open, content, projectId, analysisId, resultId]);

  const handlePrintPDF = () => {
    if (!printRef.current || !content) return;
    const printWindow = window.open('', '_blank');
    if (!printWindow) return;
    printWindow.document.write(`
      <!DOCTYPE html>
      <html>
        <head>
          <title>${title}</title>
          <style>
            body { font-family: system-ui, -apple-system, sans-serif; padding: 2rem; max-width: 800px; margin: 0 auto; }
            h1 { font-size: 1.5rem; font-weight: bold; margin-bottom: 1rem; }
            h2 { font-size: 1.25rem; font-weight: bold; margin-top: 1.5rem; margin-bottom: 0.5rem; }
            h3 { font-size: 1.1rem; font-weight: 600; margin-top: 1rem; margin-bottom: 0.25rem; }
            p { margin-bottom: 0.5rem; line-height: 1.5; }
            pre { background: #f3f4f6; padding: 1rem; border-radius: 0.25rem; overflow-x: auto; font-size: 0.8rem; }
            code { background: #f3f4f6; padding: 0.125rem 0.25rem; border-radius: 0.125rem; font-size: 0.85em; }
            ul, ol { margin-left: 1.5rem; margin-bottom: 0.5rem; }
            table { border-collapse: collapse; width: 100%; margin: 0.5rem 0; }
            th, td { border: 1px solid #e5e7eb; padding: 0.5rem; text-align: left; font-size: 0.9rem; }
            th { background: #f9fafb; font-weight: 600; }
            @media print { body { padding: 0; } }
          </style>
        </head>
        <body>
          <h1>${title}</h1>
          ${printRef.current.innerHTML}
        </body>
      </html>
    `);
    printWindow.document.close();
    printWindow.focus();
    setTimeout(() => {
      printWindow.print();
      printWindow.close();
    }, 250);
  };

  return (
    <div className="border rounded">
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center justify-between p-3 hover:bg-gray-50 text-left"
      >
        <div>
          <span className="font-medium text-sm">{open ? "▾" : "▸"} {title}</span>
          <span className="text-xs text-gray-400 ml-2">{subtitle}</span>
        </div>
        <div className="flex items-center gap-3" onClick={(e) => e.stopPropagation()}>
          <a
            href={api.analyses.downloadResult(projectId, analysisId, resultId)}
            className="text-xs text-blue-600 hover:underline"
          >
            Download Raw
          </a>
          {content && (
            <button
              onClick={handlePrintPDF}
              className="text-xs text-blue-600 hover:underline"
            >
              Download PDF
            </button>
          )}
        </div>
      </button>
      {open && (
        <div className="border-t bg-white max-h-[32rem] overflow-y-auto">
          {error ? (
            <p className="text-sm text-red-600 p-4">{error}</p>
          ) : content === null ? (
            <p className="text-sm text-gray-500 p-4">Loading...</p>
          ) : (
            <div ref={printRef} className="prose prose-sm max-w-none px-4 py-3">
              <RenderedMarkdown content={content} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** Ticking elapsed time for active analyses. */
function ElapsedTime({ since }: { since: string }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  const secs = Math.floor((now - new Date(since).getTime()) / 1000);
  if (secs < 60) return <>{secs}s elapsed</>;
  const m = Math.floor(secs / 60);
  const rem = secs % 60;
  return <>{m}m {rem}s elapsed</>;
}

function TerminalStream({
  analysisId,
  analysisStatus,
}: {
  analysisId: string;
  analysisStatus?: string;
}) {
  const [lines, setLines] = useState<string[]>([]);
  const [status, setStatus] = useState<"connecting" | "connected" | "error">(
    "connecting",
  );
  const containerRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const retryRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let cancelled = false;

    function connect() {
      if (cancelled) return;
      setStatus("connecting");
      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      const ws = new WebSocket(
        `${protocol}//${window.location.host}/ws/analysis/${analysisId}`,
      );
      wsRef.current = ws;

      ws.onopen = () => {
        if (!cancelled) setStatus("connected");
      };

      ws.onmessage = (event) => {
        if (!cancelled) {
          setStatus("connected");
          setLines((prev) => [...prev, event.data]);
        }
      };

      ws.onclose = () => {
        if (!cancelled) {
          retryRef.current = setTimeout(connect, 3000);
        }
      };

      ws.onerror = () => {
        if (!cancelled) setStatus("error");
      };
    }

    connect();

    return () => {
      cancelled = true;
      if (retryRef.current) clearTimeout(retryRef.current);
      if (wsRef.current) wsRef.current.close();
    };
  }, [analysisId]);

  useEffect(() => {
    if (containerRef.current)
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
  }, [lines]);

  return (
    <div className="mb-6">
      <div className="flex items-center gap-2 mb-2">
        <h3 className="font-medium">Live Output</h3>
        <span
          className={`inline-block w-2 h-2 rounded-full animate-pulse ${
            status === "connected"
              ? "bg-green-400"
              : status === "error"
                ? "bg-red-400"
                : "bg-yellow-400"
          }`}
        />
      </div>
      <div
        ref={containerRef}
        className="bg-gray-950 p-4 rounded-lg border border-gray-800 max-h-[32rem] overflow-y-auto overflow-x-hidden space-y-1"
      >
        {lines.length === 0 ? (
          <div className="text-gray-500 italic text-sm flex items-center gap-2">
            {analysisStatus === "failed" || analysisStatus === "cancelled" || analysisStatus === "timed_out" ? (
              <>Worker exited before producing output.</>
            ) : status === "connecting" ? (
              <>Connecting to analysis stream...</>
            ) : status === "error" ? (
              <>Waiting for analysis to start... (reconnecting)</>
            ) : (
              <>Connected — waiting for agent output...</>
            )}
          </div>
        ) : (
          lines.map((line, i) => <StreamLine key={i} line={line} />)
        )}
      </div>
    </div>
  );
}

/** Renders a single line from the WebSocket stream with appropriate styling. */
function ArchivedOutput({
  logs,
  projectId,
  analysisId,
}: {
  logs: AnalysisResult[];
  projectId: string;
  analysisId: string;
}) {
  // Show stdout expanded by default, others collapsed
  const stdoutLog = logs.find((l) => l.filename === "agent_stdout.log");
  const otherLogs = logs.filter((l) => l.filename !== "agent_stdout.log");
  const [showOther, setShowOther] = useState<string | null>(null);

  return (
    <div className="mb-6 space-y-3">
      {stdoutLog && (
        <div>
          <div className="flex items-center justify-between mb-1">
            <h3 className="font-medium">Output</h3>
            <a
              href={api.analyses.downloadResult(
                projectId,
                analysisId,
                stdoutLog.id,
              )}
              className="text-xs text-gray-500 hover:text-blue-600"
            >
              Download
            </a>
          </div>
          <LogContent
            projectId={projectId}
            analysisId={analysisId}
            resultId={stdoutLog.id}
          />
        </div>
      )}
      {otherLogs.map((log) => (
        <div key={log.id}>
          <div className="flex items-center justify-between">
            <button
              onClick={() =>
                setShowOther(showOther === log.id ? null : log.id)
              }
              className="text-sm font-medium text-blue-600 hover:underline"
            >
              {showOther === log.id ? "▾" : "▸"} {log.filename}
              <span className="text-gray-400 ml-1 text-xs font-normal">
                ({(log.file_size / 1024).toFixed(1)} KB)
              </span>
            </button>
            <a
              href={api.analyses.downloadResult(
                projectId,
                analysisId,
                log.id,
              )}
              className="text-xs text-gray-500 hover:text-blue-600"
            >
              Download
            </a>
          </div>
          {showOther === log.id && (
            <LogContent
              projectId={projectId}
              analysisId={analysisId}
              resultId={log.id}
            />
          )}
        </div>
      ))}
    </div>
  );
}

function LogContent({
  projectId,
  analysisId,
  resultId,
}: {
  projectId: string;
  analysisId: string;
  resultId: string;
}) {
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [viewMode, setViewMode] = useState<"formatted" | "raw">("formatted");
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const url = api.analyses.downloadResult(projectId, analysisId, resultId);
    fetch(url, { credentials: "include" })
      .then((r) => {
        if (!r.ok) throw new Error("Failed to load log");
        return r.text();
      })
      .then((text) => setContent(text))
      .catch((err) => setError(err.message));
  }, [projectId, analysisId, resultId]);

  if (error) return <p className="text-sm text-red-600 px-3 pb-2">{error}</p>;
  if (content === null)
    return <p className="text-sm text-gray-500 px-3 pb-2">Loading...</p>;

  const rawLines = content.split("\n");
  const formattedLines = processLogLines(rawLines);

  return (
    <div>
      <div className="flex justify-end mb-1">
        <div className="inline-flex rounded border border-gray-300 text-xs overflow-hidden">
          <button
            onClick={() => setViewMode("formatted")}
            className={`px-2.5 py-1 ${viewMode === "formatted" ? "bg-gray-800 text-white" : "bg-white text-gray-600 hover:bg-gray-100"}`}
          >
            Formatted
          </button>
          <button
            onClick={() => setViewMode("raw")}
            className={`px-2.5 py-1 border-l border-gray-300 ${viewMode === "raw" ? "bg-gray-800 text-white" : "bg-white text-gray-600 hover:bg-gray-100"}`}
          >
            Raw
          </button>
        </div>
      </div>
      <div
        ref={containerRef}
        className="bg-gray-950 p-4 rounded-lg border border-gray-800 max-h-96 overflow-y-auto overflow-x-hidden space-y-1"
      >
        {viewMode === "raw"
          ? rawLines.map((line, i) => (
              <div
                key={i}
                className="text-green-400 font-mono text-xs whitespace-pre-wrap break-words"
              >
                {line || "\u00A0"}
              </div>
            ))
          : formattedLines.length > 0
            ? formattedLines.map((line, i) => (
                <StreamLine key={i} line={line} />
              ))
            : rawLines.map((line, i) => (
                <div
                  key={i}
                  className="text-green-400 font-mono text-xs whitespace-pre-wrap break-words"
                >
                  {line || "\u00A0"}
                </div>
              ))}
      </div>
    </div>
  );
}
