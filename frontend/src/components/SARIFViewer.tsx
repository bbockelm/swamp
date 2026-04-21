'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { api, type Finding } from '@/lib/api';

interface SARIFRun {
  tool?: { driver?: { name?: string } };
  results?: SARIFResult[];
}

interface SARIFResult {
  ruleId?: string;
  level?: string;
  message?: { text?: string };
  locations?: Array<{
    physicalLocation?: {
      artifactLocation?: { uri?: string };
      region?: { startLine?: number };
    };
  }>;
}

interface SARIFLog {
  runs?: SARIFRun[];
}

const levelColors: Record<string, string> = {
  error: 'bg-red-100 text-red-800',
  warning: 'bg-yellow-100 text-yellow-800',
  note: 'bg-blue-100 text-blue-800',
};

function findingKey(ruleId: string, filePath: string, startLine: number): string {
  return `${ruleId}||${filePath}||${startLine}`;
}

export function SARIFViewer({
  projectId,
  analysisId,
  resultId,
  onFindingsCountChange,
}: {
  projectId: string;
  analysisId: string;
  resultId: string;
  onFindingsCountChange?: (count: number) => void;
}) {
  const router = useRouter();
  const [sarif, setSarif] = useState<SARIFLog | null>(null);
  const [findings, setFindings] = useState<Map<string, Finding>>(new Map());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    const url = api.analyses.downloadResult(projectId, analysisId, resultId);
    fetch(url, { credentials: 'include' })
      .then((r) => {
        if (!r.ok) throw new Error('Failed to load SARIF');
        return r.json();
      })
      .then((data) => {
        setSarif(data);
        const runs = (data?.runs || []) as SARIFRun[];
        const count = runs.flatMap((run) => run.results || []).length;
        onFindingsCountChange?.(count);
        setLoading(false);
      })
      .catch((err) => {
        setError(err.message);
        onFindingsCountChange?.(0);
        setLoading(false);
      });
  }, [projectId, analysisId, resultId, onFindingsCountChange]);

  // Fetch findings for this analysis to create links.
  useEffect(() => {
    api.findings.list(projectId, { analysis_id: analysisId, limit: 500 })
      .then((resp) => {
        const map = new Map<string, Finding>();
        for (const f of resp.findings) {
          map.set(findingKey(f.rule_id, f.file_path, f.start_line), f);
        }
        setFindings(map);
      })
      .catch((err) => { console.warn('Failed to load findings for linking:', err); });
  }, [projectId, analysisId]);

  if (loading) return <p className="text-sm text-gray-500">Loading SARIF...</p>;
  if (error) return <p className="text-sm text-red-600">{error}</p>;
  if (!sarif?.runs?.length) return <p className="text-sm text-gray-500">No findings.</p>;

  const allResults = sarif.runs.flatMap((run) => run.results || []);

  if (!allResults.length) {
    return (
      <div className="bg-green-50 border border-green-200 p-4 rounded text-green-800 text-sm">
        No security findings detected.
      </div>
    );
  }

  return (
    <div className="border rounded overflow-x-auto">
      <table className="w-full text-sm">
        <thead className="bg-gray-50 border-b">
          <tr>
            <th className="text-left px-4 py-2 font-medium">Severity</th>
            <th className="text-left px-4 py-2 font-medium hidden sm:table-cell">Rule</th>
            <th className="text-left px-4 py-2 font-medium">Location</th>
            <th className="text-left px-4 py-2 font-medium">Message</th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {allResults.map((result, idx) => {
            const loc = result.locations?.[0]?.physicalLocation;
            const file = loc?.artifactLocation?.uri || '';
            const line = loc?.region?.startLine;
            const level = result.level || 'note';
            const key = findingKey(result.ruleId || '', file, line || 0);
            const finding = findings.get(key);

            const findingUrl = finding
              ? `/projects/${projectId}?tab=findings&analysis=${analysisId}&finding=${finding.id}`
              : null;

            return (
              <tr
                key={idx}
                className={`hover:bg-gray-50${finding ? ' cursor-pointer' : ''}`}
                onClick={finding ? () => router.push(findingUrl!) : undefined}
              >
                <td className="px-4 py-2">
                  <span
                    className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${levelColors[level] || 'bg-gray-100 text-gray-800'}`}
                  >
                    {level}
                  </span>
                </td>
                <td className="px-4 py-2 font-mono text-xs hidden sm:table-cell">
                  {result.ruleId || '-'}
                </td>
                <td className="px-4 py-2 font-mono text-xs text-gray-600">
                  {file}
                  {line != null && `:${line}`}
                </td>
                <td className="px-4 py-2 text-gray-700">
                  {result.message?.text || '-'}
                  {finding && (
                    <span className="ml-2 text-brand-500 text-xs">→</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
