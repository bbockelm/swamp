'use client';

import { useState, useMemo, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, type Finding, type FindingAnnotation } from '@/lib/api';
import { Pagination } from '@/components/Pagination';

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

function uploadStatus(f: Finding): { label: string; color: string } {
  if (!f.sarif_upload_attempted) {
    return { label: 'Not attempted', color: 'bg-gray-100 text-gray-700' };
  }
  if (f.sarif_upload_url) {
    return { label: 'Uploaded', color: 'bg-green-100 text-green-700' };
  }
  if (f.sarif_upload_error) {
    return { label: 'Failed', color: 'bg-red-100 text-red-700' };
  }
  return { label: 'Attempted', color: 'bg-amber-100 text-amber-800' };
}

function buildGitHubLink(gitUrl: string, filePath: string, line?: number, commitSha?: string): string | null {
  const match = gitUrl.match(/github\.com\/([^/]+\/[^/]+?)(?:\.git)?$/);
  if (!match) return null;
  const repo = match[1];
  const ref = commitSha || 'HEAD';
  let url = `https://github.com/${repo}/blob/${ref}/${filePath}`;
  if (line && line > 0) url += `#L${line}`;
  return url;
}

type SortField = 'level' | 'status' | 'rule_id' | 'file_path' | 'message';
type SortDir = 'asc' | 'desc';

function SortIcon({ field, sortField, sortDir }: { field: SortField; sortField: SortField | null; sortDir: SortDir }) {
  if (sortField !== field) {
    return <span className="ml-1 text-gray-300">&#8597;</span>;
  }
  return <span className="ml-1">{sortDir === 'asc' ? '▲' : '▼'}</span>;
}

export function FindingsTable({
  projectId,
  gitUrl,
  initialLevel,
  initialAnalysisId,
  initialFindingId,
  canEdit = true,
}: {
  projectId: string;
  gitUrl?: string;
  initialLevel?: string;
  initialAnalysisId?: string;
  initialFindingId?: string;
  canEdit?: boolean;
}) {
  const [page, setPage] = useState(1);
  const [levelFilter, setLevelFilter] = useState(initialLevel || '');
  const [statusFilter, setStatusFilter] = useState('');
  const [ruleFilter, setRuleFilter] = useState('');
  const [fileFilter, setFileFilter] = useState('');
  const [analysisFilter, setAnalysisFilter] = useState(initialAnalysisId || '');
  const [searchText, setSearchText] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [expandedId, setExpandedId] = useState<string | null>(initialFindingId || null);
  const [sortField, setSortField] = useState<SortField | null>(null);
  const [sortDir, setSortDir] = useState<SortDir>('asc');

  // Debounce search input
  useEffect(() => {
    const id = setTimeout(() => setDebouncedSearch(searchText), 300);
    return () => clearTimeout(id);
  }, [searchText]);

  const offset = (page - 1) * PAGE_SIZE;

  const { data, isLoading } = useQuery({
    queryKey: [
      'findings',
      projectId,
      levelFilter,
      statusFilter,
      ruleFilter,
      fileFilter,
      analysisFilter,
      debouncedSearch,
      offset,
    ],
    queryFn: () =>
      api.findings.list(projectId, {
        level: levelFilter || undefined,
        status: statusFilter || undefined,
        rule_id: ruleFilter || undefined,
        file_path: fileFilter || undefined,
        analysis_id: analysisFilter || undefined,
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

  // Client-side sort on current page of results
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
        {(levelFilter || statusFilter || ruleFilter || fileFilter || analysisFilter || searchText) && (
          <button
            onClick={() => {
              setLevelFilter('');
              setStatusFilter('');
              setRuleFilter('');
              setFileFilter('');
              setAnalysisFilter('');
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

      {/* Results summary */}
      <div className="text-sm text-gray-500 mb-3">
        {data ? `${data.total} finding${data.total !== 1 ? 's' : ''}` : 'Loading...'}
        {analysisFilter && (
          <span className="ml-2 text-xs bg-blue-50 text-blue-700 px-2 py-0.5 rounded">
            Analysis {analysisFilter.slice(0, 8)}
            <button onClick={() => { setAnalysisFilter(''); setPage(1); }} className="ml-1 text-blue-400 hover:text-blue-600">&times;</button>
          </span>
        )}
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
        <div className="border rounded overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 border-b">
              <tr>
                <th className={`${thClass} w-24`} onClick={() => handleSort('level')}>
                  Severity <SortIcon field="level" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className={`${thClass} w-24`} onClick={() => handleSort('status')}>
                  Status <SortIcon field="status" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className={`${thClass} hidden sm:table-cell`} onClick={() => handleSort('rule_id')}>
                  Rule <SortIcon field="rule_id" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className={thClass} onClick={() => handleSort('file_path')}>
                  Location <SortIcon field="file_path" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className={thClass} onClick={() => handleSort('message')}>
                  Message <SortIcon field="message" sortField={sortField} sortDir={sortDir} />
                </th>
                <th className="text-left px-4 py-2 font-medium w-36">GitHub Upload</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {sortedFindings.map((f) => (
                <FindingRow
                  key={f.id}
                  finding={f}
                  projectId={projectId}
                  gitUrl={gitUrl}
                  expanded={expandedId === f.id}
                  onToggle={() => setExpandedId(expandedId === f.id ? null : f.id)}
                  canEdit={canEdit}
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

function FindingRow({
  finding,
  projectId,
  gitUrl,
  expanded,
  onToggle,
  canEdit,
}: {
  finding: Finding;
  projectId: string;
  gitUrl?: string;
  expanded: boolean;
  onToggle: () => void;
  canEdit: boolean;
}) {
  const level = finding.level || 'note';
  const ghLink = gitUrl ? buildGitHubLink(gitUrl, finding.file_path, finding.start_line, finding.git_commit) : null;
  const upload = uploadStatus(finding);

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
        <td className="px-4 py-2 font-mono text-xs hidden sm:table-cell">{finding.rule_id || '-'}</td>
        <td className="px-4 py-2 font-mono text-xs text-gray-600">
          {ghLink ? (
            <a
              href={ghLink}
              target="_blank"
              rel="noopener noreferrer"
              className="text-blue-600 hover:underline"
              onClick={(e) => e.stopPropagation()}
            >
              {finding.file_path}{finding.start_line > 0 && `:${finding.start_line}`}
            </a>
          ) : (
            <>
              {finding.file_path}
              {finding.start_line > 0 && `:${finding.start_line}`}
            </>
          )}
        </td>
        <td className="px-4 py-2 text-gray-700 max-w-md truncate">{finding.message || '-'}</td>
        <td className="px-4 py-2">
          <span className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${upload.color}`}>
            {upload.label}
          </span>
        </td>
      </tr>
      {expanded && (
        <tr>
          <td colSpan={6} className="bg-gray-50 px-4 py-4">
            <FindingDetail finding={finding} projectId={projectId} canEdit={canEdit} />
          </td>
        </tr>
      )}
    </>
  );
}

function FindingDetail({ finding, projectId, canEdit }: { finding: Finding; projectId: string; canEdit: boolean }) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState(finding.latest_status || 'open');
  const [note, setNote] = useState(finding.latest_note || '');
  const [showAnnotations, setShowAnnotations] = useState(false);

  const annotateMutation = useMutation({
    mutationFn: () => api.findings.annotate(projectId, finding.id, { status, note }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['findings', projectId] });
    },
  });

  const { data: annotations } = useQuery({
    queryKey: ['annotations', finding.id],
    queryFn: () => api.findings.listAnnotations(projectId, finding.id),
    enabled: showAnnotations,
  });

  return (
    <div className="space-y-4">
      {/* Details */}
      <div className="grid grid-cols-2 gap-4 text-sm">
        <div>
          <span className="text-gray-500">Rule:</span>{' '}
          <span className="font-mono">{finding.rule_id || '-'}</span>
        </div>
        <div>
          <span className="text-gray-500">Analysis:</span>{' '}
          <span className="font-mono">{finding.analysis_id.slice(0, 8)}</span>
        </div>
        <div>
          <span className="text-gray-500">File:</span>{' '}
          <span className="font-mono">{finding.file_path}</span>
        </div>
        <div>
          <span className="text-gray-500">Lines:</span>{' '}
          {finding.start_line}
          {finding.end_line > 0 && finding.end_line !== finding.start_line && `–${finding.end_line}`}
        </div>
        <div>
          <span className="text-gray-500">GitHub Upload:</span>{' '}
          <span className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${uploadStatus(finding).color}`}>
            {uploadStatus(finding).label}
          </span>
        </div>
        {finding.sarif_upload_url && (
          <div className="col-span-2">
            <span className="text-gray-500">Code Scanning:</span>{' '}
            <a
              href={finding.sarif_upload_url}
              target="_blank"
              rel="noopener noreferrer"
              className="text-blue-600 hover:underline"
            >
              View alerts ↗
            </a>
          </div>
        )}
        {finding.sarif_upload_attempted && finding.sarif_upload_error && (
          <div className="col-span-2 text-red-700 font-mono break-words">
            <span className="text-gray-500 font-sans">Upload error:</span>{' '}
            {finding.sarif_upload_error}
          </div>
        )}
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
      {canEdit && (
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
          {annotateMutation.isError && (
            <p className="text-red-600 text-xs mt-1">Failed to save annotation.</p>
          )}
        </div>
      )}

      {/* Annotation history toggle */}
      <div>
        <button
          onClick={() => setShowAnnotations(!showAnnotations)}
          className="text-xs text-blue-600 hover:underline"
        >
          {showAnnotations ? 'Hide' : 'Show'} annotation history
        </button>
        {showAnnotations && annotations && annotations.length > 0 && (
          <div className="mt-2 space-y-2">
            {annotations.map((a: FindingAnnotation) => (
              <div key={a.id} className="text-xs border rounded p-2 bg-white">
                <div className="flex justify-between">
                  <span className="font-medium">{a.user_display_name || 'Unknown'}</span>
                  <span className="text-gray-400">
                    {new Date(a.updated_at).toLocaleString()}
                  </span>
                </div>
                <div className="mt-1">
                  <span className={`inline-block px-1.5 py-0.5 rounded text-xs ${statusColor(a.status)}`}>
                    {statusLabel(a.status)}
                  </span>
                  {a.note && <span className="ml-2 text-gray-600">{a.note}</span>}
                </div>
              </div>
            ))}
          </div>
        )}
        {showAnnotations && (!annotations || annotations.length === 0) && (
          <p className="text-xs text-gray-400 mt-1">No annotations yet.</p>
        )}
      </div>
    </div>
  );
}
