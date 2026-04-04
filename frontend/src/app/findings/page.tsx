'use client';

import { Suspense, useState, useMemo, useEffect } from 'react';
import { useSearchParams } from 'next/navigation';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, type Finding } from '@/lib/api';
import { Pagination } from '@/components/Pagination';
import Link from 'next/link';

const PAGE_SIZE = 25;

const LEVEL_COLORS: Record<string, string> = {
  error: 'bg-red-100 text-red-800',
  warning: 'bg-amber-100 text-amber-800',
  note: 'bg-blue-100 text-blue-800',
};

const LEVEL_LABELS: Record<string, string> = {
  error: 'High',
  warning: 'Medium',
  note: 'Low',
};

const LEVEL_ORDER: Record<string, number> = {
  error: 1,
  warning: 2,
  note: 3,
};

function buildGitHubLink(gitUrl: string, filePath: string, line?: number): string | null {
  const match = gitUrl.match(/github\.com\/([^/]+\/[^/]+?)(?:\.git)?$/);
  if (!match) return null;
  const repo = match[1];
  let url = `https://github.com/${repo}/blob/HEAD/${filePath}`;
  if (line && line > 0) url += `#L${line}`;
  return url;
}

const STATUS_OPTIONS = [
  { value: 'open', label: 'Open', color: 'bg-gray-100 text-gray-800' },
  { value: 'confirmed', label: 'Confirmed', color: 'bg-red-100 text-red-800' },
  { value: 'false_positive', label: 'False Positive', color: 'bg-green-100 text-green-700' },
  { value: 'not_relevant', label: 'Not Relevant', color: 'bg-gray-100 text-gray-600' },
  { value: 'wont_fix', label: "Won't Fix", color: 'bg-yellow-100 text-yellow-800' },
  { value: 'mitigated', label: 'Mitigated', color: 'bg-blue-100 text-blue-800' },
];

function statusColor(status: string): string {
  return STATUS_OPTIONS.find((s) => s.value === status)?.color || 'bg-gray-100 text-gray-800';
}

function statusLabel(status: string): string {
  return STATUS_OPTIONS.find((s) => s.value === status)?.label || status;
}

type SortField = 'level' | 'status' | 'rule_id' | 'file_path' | 'message';
type SortDir = 'asc' | 'desc';

function SortIcon({ field, sortField, sortDir }: { field: SortField; sortField: SortField | null; sortDir: SortDir }) {
  if (sortField !== field) {
    return <span className="ml-1 text-gray-300">&#8597;</span>;
  }
  return <span className="ml-1">{sortDir === 'asc' ? '▲' : '▼'}</span>;
}

// Map dashboard severity names to SARIF levels.
function severityToLevel(sev: string): string {
  switch (sev.toLowerCase()) {
    case 'critical':
    case 'high':
    case 'error':
      return 'error';
    case 'medium':
    case 'warning':
      return 'warning';
    case 'low':
    case 'note':
    case 'info':
      return 'note';
    default:
      return '';
  }
}

export default function FindingsPage() {
  return (
    <Suspense fallback={<div className="p-8 text-gray-400">Loading findings...</div>}>
      <FindingsPageInner />
    </Suspense>
  );
}

function FindingsPageInner() {
  const searchParams = useSearchParams();
  const initialSeverity = searchParams.get('severity') || '';
  const initialLevel = initialSeverity ? severityToLevel(initialSeverity) : '';

  const [page, setPage] = useState(1);
  const [levelFilter, setLevelFilter] = useState(initialLevel);
  const [statusFilter, setStatusFilter] = useState('');
  const [ruleFilter, setRuleFilter] = useState('');
  const [fileFilter, setFileFilter] = useState('');
  const [searchText, setSearchText] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [sortField, setSortField] = useState<SortField | null>(null);
  const [sortDir, setSortDir] = useState<SortDir>('asc');

  useEffect(() => {
    const id = setTimeout(() => setDebouncedSearch(searchText), 300);
    return () => clearTimeout(id);
  }, [searchText]);

  const offset = (page - 1) * PAGE_SIZE;

  const { data, isLoading } = useQuery({
    queryKey: ['all-findings', levelFilter, statusFilter, ruleFilter, fileFilter, debouncedSearch, offset],
    queryFn: () =>
      api.findings.listAll({
        level: levelFilter || undefined,
        status: statusFilter || undefined,
        rule_id: ruleFilter || undefined,
        file_path: fileFilter || undefined,
        search: debouncedSearch || undefined,
        limit: PAGE_SIZE,
        offset,
      }),
  });

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortDir(sortDir === 'asc' ? 'desc' : 'asc');
    } else {
      setSortField(field);
      setSortDir('asc');
    }
  };

  const findings = useMemo(() => data?.findings ?? [], [data]);

  const sortedFindings = useMemo(() => {
    if (!sortField) return findings;
    const sorted = [...findings].sort((a, b) => {
      let cmp = 0;
      switch (sortField) {
        case 'level':
          cmp = (LEVEL_ORDER[a.level] || 4) - (LEVEL_ORDER[b.level] || 4);
          break;
        case 'status': {
          const sa = statusLabel(a.latest_status || 'open');
          const sb = statusLabel(b.latest_status || 'open');
          cmp = sa.localeCompare(sb);
          break;
        }
        case 'rule_id':
          cmp = (a.rule_id || '').localeCompare(b.rule_id || '');
          break;
        case 'file_path': {
          const fa = `${a.file_path}:${a.start_line}`;
          const fb = `${b.file_path}:${b.start_line}`;
          cmp = fa.localeCompare(fb);
          break;
        }
        case 'message':
          cmp = (a.message || '').localeCompare(b.message || '');
          break;
      }
      return sortDir === 'asc' ? cmp : -cmp;
    });
    return sorted;
  }, [findings, sortField, sortDir]);

  const totalPages = data ? Math.ceil(data.total / PAGE_SIZE) : 1;
  const thClass = "text-left px-4 py-2 font-medium cursor-pointer select-none hover:bg-gray-100 transition-colors";

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">All Findings</h1>

      {/* Search + Filters */}
      <div className="flex flex-wrap gap-3 mb-4 items-center">
        <div className="relative flex-1 min-w-[200px] max-w-md">
          <input
            type="text"
            value={searchText}
            onChange={(e) => { setSearchText(e.target.value); setPage(1); }}
            placeholder="Search findings..."
            className="border rounded px-3 py-1.5 text-sm w-full pl-8"
          />
          <svg className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
        </div>
        <select
          value={levelFilter}
          onChange={(e) => { setLevelFilter(e.target.value); setPage(1); }}
          className="border rounded px-2 py-1.5 text-sm"
        >
          <option value="">All Severities</option>
          <option value="error">High</option>
          <option value="warning">Medium</option>
          <option value="note">Low</option>
        </select>
        <select
          value={statusFilter}
          onChange={(e) => { setStatusFilter(e.target.value); setPage(1); }}
          className="border rounded px-2 py-1.5 text-sm"
        >
          <option value="">All Statuses</option>
          {STATUS_OPTIONS.map((s) => (
            <option key={s.value} value={s.value}>{s.label}</option>
          ))}
        </select>
        <input
          type="text"
          value={ruleFilter}
          onChange={(e) => { setRuleFilter(e.target.value); setPage(1); }}
          placeholder="Rule ID..."
          className="border rounded px-2 py-1.5 text-sm w-36"
        />
        <input
          type="text"
          value={fileFilter}
          onChange={(e) => { setFileFilter(e.target.value); setPage(1); }}
          placeholder="File path..."
          className="border rounded px-2 py-1.5 text-sm w-48"
        />
        {(levelFilter || statusFilter || ruleFilter || fileFilter || searchText) && (
          <button
            onClick={() => {
              setLevelFilter('');
              setStatusFilter('');
              setRuleFilter('');
              setFileFilter('');
              setSearchText('');
              setDebouncedSearch('');
              setPage(1);
            }}
            className="text-sm text-blue-600 hover:underline"
          >
            Clear filters
          </button>
        )}
      </div>

      <div className="text-sm text-gray-500 mb-3">
        {data ? `${data.total} finding${data.total !== 1 ? 's' : ''}` : 'Loading...'}
        {sortField && (
          <button
            onClick={() => { setSortField(null); }}
            className="ml-2 text-xs text-blue-600 hover:underline"
          >
            Clear sort
          </button>
        )}
      </div>

      {isLoading ? (
        <div className="animate-pulse space-y-2">
          {[...Array(5)].map((_, i) => (
            <div key={i} className="h-12 bg-gray-100 rounded" />
          ))}
        </div>
      ) : !data?.findings?.length ? (
        <div className="text-center py-8 text-gray-400 text-sm">
          No findings match the current filters.
        </div>
      ) : (
        <div className="border rounded overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 border-b">
              <tr>
                <th className={`${thClass} w-24`} onClick={() => handleSort('level')}>
                  Severity <SortIcon field="level" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className={`${thClass} w-24`} onClick={() => handleSort('status')}>
                  Status <SortIcon field="status" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className={thClass} onClick={() => handleSort('rule_id')}>
                  Rule <SortIcon field="rule_id" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className={thClass} onClick={() => handleSort('file_path')}>
                  Location <SortIcon field="file_path" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className={thClass} onClick={() => handleSort('message')}>
                  Message <SortIcon field="message" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className="text-left px-4 py-2 font-medium w-20">Project</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {sortedFindings.map((f) => (
                <GlobalFindingRow
                  key={f.id}
                  finding={f}
                  expanded={expandedId === f.id}
                  onToggle={() => setExpandedId(expandedId === f.id ? null : f.id)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {totalPages > 1 && (
        <Pagination
          currentPage={page}
          totalPages={totalPages}
          onPageChange={setPage}
        />
      )}
    </div>
  );
}

function GlobalFindingRow({
  finding,
  expanded,
  onToggle,
}: {
  finding: Finding;
  expanded: boolean;
  onToggle: () => void;
}) {
  const level = finding.level || 'note';
  return (
    <>
      <tr className="hover:bg-gray-50 cursor-pointer" onClick={onToggle}>
        <td className="px-4 py-2">
          <span className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${LEVEL_COLORS[level] || 'bg-gray-100 text-gray-800'}`}>
            {LEVEL_LABELS[level] || level}
          </span>
        </td>
        <td className="px-4 py-2">
          <span className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${statusColor(finding.latest_status || 'open')}`}>
            {statusLabel(finding.latest_status || 'open')}
          </span>
        </td>
        <td className="px-4 py-2 font-mono text-xs">{finding.rule_id || '-'}</td>
        <td className="px-4 py-2 font-mono text-xs text-gray-600 max-w-xs truncate">
          {(() => {
            const ghLink = finding.git_url ? buildGitHubLink(finding.git_url, finding.file_path, finding.start_line) : null;
            const label = `${finding.file_path}${finding.start_line > 0 ? `:${finding.start_line}` : ''}`;
            return ghLink ? (
              <a href={ghLink} target="_blank" rel="noopener noreferrer" className="text-blue-600 hover:underline" onClick={(e) => e.stopPropagation()}>{label}</a>
            ) : label;
          })()}
        </td>
        <td className="px-4 py-2 text-gray-700 max-w-md truncate">{finding.message || '-'}</td>
        <td className="px-4 py-2">
          <Link
            href={`/projects/${finding.project_id}?tab=findings`}
            className="text-blue-600 hover:underline text-xs"
            onClick={(e) => e.stopPropagation()}
          >
            View
          </Link>
        </td>
      </tr>
      {expanded && (
        <tr>
          <td colSpan={6} className="bg-gray-50 px-4 py-4">
            <GlobalFindingDetail finding={finding} />
          </td>
        </tr>
      )}
    </>
  );
}

function GlobalFindingDetail({ finding }: { finding: Finding }) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState(finding.latest_status || 'open');
  const [note, setNote] = useState(finding.latest_note || '');

  const annotateMutation = useMutation({
    mutationFn: () => api.findings.annotate(finding.project_id, finding.id, { status, note }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['all-findings'] });
    },
  });

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-4 text-sm">
        <div>
          <span className="text-gray-500">Rule:</span>{' '}
          <span className="font-mono">{finding.rule_id || '-'}</span>
        </div>
        <div>
          <span className="text-gray-500">Analysis:</span>{' '}
          <Link
            href={`/projects/${finding.project_id}/analyses/${finding.analysis_id}`}
            className="font-mono text-blue-600 hover:underline"
          >
            {finding.analysis_id.slice(0, 8)}
          </Link>
        </div>
        <div>
          <span className="text-gray-500">File:</span>{' '}
          {(() => {
            const ghLink = finding.git_url ? buildGitHubLink(finding.git_url, finding.file_path, finding.start_line) : null;
            return ghLink ? (
              <a href={ghLink} target="_blank" rel="noopener noreferrer" className="font-mono text-blue-600 hover:underline">{finding.file_path}</a>
            ) : (
              <span className="font-mono">{finding.file_path}</span>
            );
          })()}
        </div>
        <div>
          <span className="text-gray-500">Lines:</span>{' '}
          {finding.start_line}
          {finding.end_line > 0 && finding.end_line !== finding.start_line && `–${finding.end_line}`}
        </div>
      </div>

      {finding.message && (
        <div className="text-sm">
          <span className="text-gray-500">Message:</span>
          <p className="mt-1">{finding.message}</p>
        </div>
      )}

      {finding.snippet && (
        <pre className="text-xs bg-gray-100 border rounded p-3 overflow-x-auto">{finding.snippet}</pre>
      )}

      {/* Annotation form */}
      <div className="border-t pt-4">
        <h4 className="text-sm font-medium mb-2">Annotate Finding</h4>
        <div className="flex gap-3 items-end">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Status</label>
            <select
              value={status}
              onChange={(e) => setStatus(e.target.value)}
              className="border rounded px-2 py-1.5 text-sm"
            >
              {STATUS_OPTIONS.map((s) => (
                <option key={s.value} value={s.value}>{s.label}</option>
              ))}
            </select>
          </div>
          <div className="flex-1">
            <label className="block text-xs text-gray-500 mb-1">Note</label>
            <input
              type="text"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              className="border rounded px-2 py-1.5 text-sm w-full"
              placeholder="Add a note..."
            />
          </div>
          <button
            onClick={() => annotateMutation.mutate()}
            disabled={annotateMutation.isPending}
            className="bg-blue-600 text-white px-3 py-1.5 text-sm rounded hover:bg-blue-700 disabled:opacity-50"
          >
            {annotateMutation.isPending ? 'Saving...' : 'Save'}
          </button>
        </div>
        {annotateMutation.isSuccess && (
          <p className="text-green-600 text-xs mt-1">Annotation saved.</p>
        )}
      </div>
    </div>
  );
}
