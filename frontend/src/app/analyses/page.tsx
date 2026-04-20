'use client';

import { useState, useEffect } from 'react';
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { api, Analysis } from '@/lib/api';
import { AnalysisStatus } from '@/components/AnalysisStatus';
import { Pagination, paginate } from '@/components/Pagination';

const PAGE_SIZE = 15;

function timeAgo(dateStr: string | null): string {
  if (!dateStr) return '—';
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

function formatDuration(a: Analysis): string | null {
  if (!a.started_at) return null;
  const start = new Date(a.started_at).getTime();
  const end = a.completed_at ? new Date(a.completed_at).getTime() : Date.now();
  const secs = Math.floor((end - start) / 1000);
  if (secs < 60) return `${secs}s`;
  const m = Math.floor(secs / 60);
  const rem = secs % 60;
  if (m < 60) return `${m}m ${rem}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

function humanDelta(from: string, to: string): string {
  const secs = Math.floor((new Date(to).getTime() - new Date(from).getTime()) / 1000);
  if (secs < 60) return `${secs}s`;
  const m = Math.floor(secs / 60);
  const rem = secs % 60;
  if (m < 60) return `${m}m ${rem}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

function ElapsedTime({ since }: { since: string }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  const secs = Math.floor((now - new Date(since).getTime()) / 1000);
  if (secs < 60) return <>{secs}s</>;
  const m = Math.floor(secs / 60);
  const rem = secs % 60;
  return <>{m}m {rem}s</>;
}

type StatusFilter = 'all' | 'running' | 'pending' | 'completed' | 'failed' | 'cancelled' | 'timed_out';

export default function AnalysesPage() {
  const queryClient = useQueryClient();
  const [filter, setFilter] = useState<StatusFilter>('all');
  const [page, setPage] = useState(1);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const { data: analyses, isLoading } = useQuery({
    queryKey: ['analyses', 'all'],
    queryFn: api.analyses.listAll,
  });

  const hasActive = analyses?.some((a) => a.status === 'running' || a.status === 'pending');
  useEffect(() => {
    const interval = setInterval(
      () => queryClient.invalidateQueries({ queryKey: ['analyses', 'all'] }),
      hasActive ? 3000 : 15000
    );
    return () => clearInterval(interval);
  }, [hasActive, queryClient]);

  const filtered = analyses?.filter((a) =>
    filter === 'all' ? true : a.status === filter
  ) ?? [];

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const safeCurrentPage = Math.min(page, totalPages);
  const paged = paginate(filtered, safeCurrentPage, PAGE_SIZE);

  const counts = {
    all: analyses?.length ?? 0,
    running: analyses?.filter((a) => a.status === 'running').length ?? 0,
    pending: analyses?.filter((a) => a.status === 'pending').length ?? 0,
    completed: analyses?.filter((a) => a.status === 'completed').length ?? 0,
    failed: analyses?.filter((a) => a.status === 'failed').length ?? 0,
    cancelled: analyses?.filter((a) => a.status === 'cancelled').length ?? 0,
    timed_out: analyses?.filter((a) => a.status === 'timed_out').length ?? 0,
  };

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Analyses</h1>

      {/* Filter tabs */}
      <div className="flex flex-wrap gap-1 mb-4">
        {(Object.keys(counts) as StatusFilter[]).map((key) => (
          <button
            key={key}
            onClick={() => { setFilter(key); setPage(1); }}
            className={`text-xs px-3 py-1.5 rounded-full border transition-colors ${
              filter === key
                ? 'bg-brand-600 text-white border-brand-600'
                : 'bg-white text-gray-600 hover:bg-gray-50'
            }`}
          >
            {key.charAt(0).toUpperCase() + key.slice(1)}
            {counts[key] > 0 && (
              <span className="ml-1 opacity-75">({counts[key]})</span>
            )}
          </button>
        ))}
      </div>

      {isLoading ? (
        <div className="animate-pulse space-y-2">
          {[...Array(5)].map((_, i) => (
            <div key={i} className="h-14 bg-gray-200 rounded" />
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <p className="text-gray-500 text-sm">
          {filter === 'all' ? 'No analyses yet.' : `No ${filter} analyses.`}
        </p>
      ) : (
        <div className="space-y-2">
          {paged.map((a) => (
            <AnalysisCard
              key={a.id}
              analysis={a}
              expanded={expandedId === a.id}
              onToggle={() => setExpandedId(expandedId === a.id ? null : a.id)}
            />
          ))}
          <Pagination currentPage={safeCurrentPage} totalPages={totalPages} onPageChange={setPage} />
        </div>
      )}
    </div>
  );
}

function AnalysisCard({ analysis: a, expanded, onToggle }: { analysis: Analysis; expanded: boolean; onToggle: () => void }) {
  const queryClient = useQueryClient();
  const router = useRouter();

  const { data: results } = useQuery({
    queryKey: ['analysis-results', a.id],
    queryFn: () => api.analyses.listResults(a.project_id, a.id),
    enabled: expanded && (a.status === 'completed' || a.status === 'failed' || a.status === 'timed_out'),
  });

  const resubmit = useMutation({
    mutationFn: () => api.analyses.resubmit(a.project_id, a.id),
    onSuccess: (newAnalysis) => {
      queryClient.invalidateQueries({ queryKey: ['analyses', 'all'] });
      router.push(`/projects/${a.project_id}/analyses/${newAnalysis.id}`);
    },
  });
  const cancel = useMutation({
    mutationFn: () => api.analyses.cancel(a.project_id, a.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['analyses', 'all'] });
    },
  });
  const canResubmit = a.status === 'completed' || a.status === 'failed' || a.status === 'cancelled' || a.status === 'timed_out';
  const canCancel = a.status === 'running' || a.status === 'pending';

  const sarifResult = results?.find((r) => r.result_type === 'sarif');
  const markdownResult = results?.find((r) => r.result_type === 'markdown' || r.result_type === 'markdown_report');

  return (
    <div className="border rounded bg-white">
      <button
        onClick={onToggle}
        className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-gray-50"
      >
        <div className="flex items-center gap-3">
          <span className="font-mono text-sm text-gray-600">{a.id.slice(0, 8)}</span>
          <AnalysisStatus status={a.status} />
          <Link
            href={`/projects/${a.project_id}`}
            onClick={(e) => e.stopPropagation()}
            className="text-sm text-brand-600 hover:underline"
          >
            {a.project_name || a.project_id.slice(0, 8)}
          </Link>
          {a.status_detail && (
            <span className="text-xs text-gray-400 hidden sm:inline truncate max-w-xs">{a.status_detail}</span>
          )}
        </div>
        <div className="flex items-center gap-3">
          <span className="text-xs text-gray-400">
            {a.completed_at
              ? new Date(a.completed_at).toLocaleString()
              : a.started_at
                ? new Date(a.started_at).toLocaleString()
                : new Date(a.created_at).toLocaleString()}
          </span>
          {(a.status === 'running' || a.status === 'pending') ? (
            <span className="text-xs text-gray-400 inline-flex items-center gap-1">
              <svg className="w-3 h-3 animate-spin text-brand-500" viewBox="0 0 24 24" fill="none">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
              </svg>
              <ElapsedTime since={a.started_at || a.created_at} />
            </span>
          ) : formatDuration(a) ? (
            <span className="text-xs text-gray-400">({formatDuration(a)})</span>
          ) : null}
          <Link
            href={`/projects/${a.project_id}/analyses/${a.id}`}
            onClick={(e) => e.stopPropagation()}
            className="p-1 text-gray-400 hover:text-brand-600"
            title="Open analysis page"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
            </svg>
          </Link>
          <svg
            className={`w-4 h-4 text-gray-400 transition-transform ${expanded ? "rotate-180" : ""}`}
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </button>

      {expanded && (
        <div className="border-t px-4 py-4 space-y-4 bg-gray-50">
          {/* Metadata grid */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-sm">
            <div>
              <span className="text-xs text-gray-500 uppercase">Created</span>
              <p>{new Date(a.created_at).toLocaleString()}</p>
              <p className="text-xs text-gray-400">{timeAgo(a.created_at)}</p>
            </div>
            {a.started_at && (
              <div>
                <span className="text-xs text-gray-500 uppercase">Started</span>
                <p>{new Date(a.started_at).toLocaleString()}</p>
                <p className="text-xs text-gray-400">
                  {humanDelta(a.created_at, a.started_at)} wait
                </p>
              </div>
            )}
            {a.completed_at && (
              <div>
                <span className="text-xs text-gray-500 uppercase">
                  {a.status === 'cancelled' ? 'Cancelled' : a.status === 'timed_out' ? 'Timed Out' : 'Completed'}
                </span>
                <p>{new Date(a.completed_at).toLocaleString()}</p>
                {a.started_at && (
                  <p className="text-xs text-gray-400">
                    {humanDelta(a.started_at, a.completed_at)} run
                  </p>
                )}
              </div>
            )}
            {formatDuration(a) && (
              <div>
                <span className="text-xs text-gray-500 uppercase">Total Duration</span>
                <p>{formatDuration(a)}</p>
              </div>
            )}
            <div>
              <span className="text-xs text-gray-500 uppercase">Project</span>
              <p>
                <Link href={`/projects/${a.project_id}`} className="text-brand-600 hover:underline">
                  {a.project_name || a.project_id.slice(0, 8)}
                </Link>
              </p>
            </div>
            {a.triggered_by && (
              <div>
                <span className="text-xs text-gray-500 uppercase">Triggered By</span>
                <p>
                  {a.trigger_event && a.trigger_event !== 'manual' ? (
                    <span className="inline-flex items-center gap-1">
                      <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-purple-100 text-purple-800">
                        {a.trigger_event === 'push' ? 'push' :
                         a.trigger_event === 'pull_request' ? 'PR' :
                         a.trigger_event === 'release' ? 'release' :
                         a.trigger_event}
                      </span>
                      {a.triggered_by_name || a.triggered_by.replace('webhook:', '')}
                    </span>
                  ) : (
                    a.triggered_by_name || a.triggered_by.slice(0, 8)
                  )}
                </p>
              </div>
            )}
            {a.git_branch && (
              <div>
                <span className="text-xs text-gray-500 uppercase">Branch</span>
                <p className="font-mono text-sm">{a.git_branch}</p>
              </div>
            )}
            {a.git_commit && (
              <div>
                <span className="text-xs text-gray-500 uppercase">Commit</span>
                <p className="font-mono">{a.git_commit.slice(0, 12)}</p>
              </div>
            )}
          </div>

          {a.error_message && (
            <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded p-3">
              {a.error_message}
            </div>
          )}

          {/* Results summary */}
          {results && results.length > 0 && (
            <div className="space-y-2">
              <h4 className="text-xs font-medium text-gray-500 uppercase">Results</h4>
              <div className="flex flex-wrap gap-2">
                {sarifResult && (
                  <span className="inline-flex items-center gap-1.5 text-xs bg-white border rounded px-2.5 py-1.5">
                    <svg className="w-3.5 h-3.5 text-orange-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                    {sarifResult.finding_count} finding{sarifResult.finding_count !== 1 ? 's' : ''}
                    {sarifResult.severity_counts && Object.keys(sarifResult.severity_counts).length > 0 && (
                      <span className="text-gray-400 ml-1">
                        ({Object.entries(sarifResult.severity_counts).map(([k, v]) => `${v} ${k}`).join(', ')})
                      </span>
                    )}
                  </span>
                )}
                {markdownResult && (
                  <a
                    href={api.analyses.downloadResult(a.project_id, a.id, markdownResult.id)}
                    className="inline-flex items-center gap-1.5 text-xs bg-white border rounded px-2.5 py-1.5 hover:bg-gray-50 text-brand-600"
                  >
                    <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 10v6m0 0l-3-3m3 3l3-3M3 17V7a2 2 0 012-2h6l2 2h6a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z" />
                    </svg>
                    Download Report
                  </a>
                )}
                {results.filter((r) => r.result_type !== 'sarif' && r.result_type !== 'markdown' && r.result_type !== 'markdown_report' && r.result_type !== 'agent_log').map((r) => (
                  <a
                    key={r.id}
                    href={api.analyses.downloadResult(a.project_id, a.id, r.id)}
                    className="inline-flex items-center gap-1.5 text-xs bg-white border rounded px-2.5 py-1.5 hover:bg-gray-50"
                  >
                    {r.filename}
                    <span className="text-gray-400">({(r.file_size / 1024).toFixed(1)} KB)</span>
                  </a>
                ))}
              </div>
            </div>
          )}

          {/* Actions */}
          <div className="flex items-center gap-2 pt-1">
            <Link
              href={`/projects/${a.project_id}/analyses/${a.id}`}
              className="text-xs px-3 py-1.5 rounded border text-brand-600 border-brand-300 hover:bg-brand-50"
            >
              View Full Analysis
            </Link>
            {canCancel && (
              <button
                onClick={() => cancel.mutate()}
                disabled={cancel.isPending}
                className="text-xs px-3 py-1.5 rounded border text-red-600 border-red-300 hover:bg-red-50 disabled:opacity-50"
              >
                {cancel.isPending ? 'Cancelling…' : 'Cancel'}
              </button>
            )}
            {canResubmit && (
              <button
                onClick={() => resubmit.mutate()}
                disabled={resubmit.isPending}
                className="text-xs px-3 py-1.5 rounded border text-brand-600 border-brand-300 hover:bg-brand-50 disabled:opacity-50"
              >
                {resubmit.isPending ? 'Submitting…' : 'Resubmit'}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
