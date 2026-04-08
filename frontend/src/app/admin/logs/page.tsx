'use client';

import { useState, useEffect, useRef, useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, LogEntry } from '@/lib/api';

const LEVEL_STYLES: Record<string, string> = {
  info: 'bg-blue-100 text-blue-800',
  warn: 'bg-yellow-100 text-yellow-800',
  warning: 'bg-yellow-100 text-yellow-800',
  error: 'bg-red-100 text-red-800',
  fatal: 'bg-red-200 text-red-900',
  panic: 'bg-red-300 text-red-900',
};

function formatTimestamp(ts: string): string {
  return new Date(ts).toLocaleString();
}

const ALL_LEVELS = ['info', 'warn', 'error', 'fatal'] as const;
const PAGE_SIZE = 200;

function stableKey(entry: LogEntry): string {
  return `${entry.timestamp}-${entry.level}-${entry.message}`;
}

export default function AdminLogsPage() {
  const [selectedLevels, setSelectedLevels] = useState<Set<string>>(new Set(ALL_LEVELS));
  const [search, setSearch] = useState('');
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE);
  const [expandedKeys, setExpandedKeys] = useState<Set<string>>(new Set());
  const sentinelRef = useRef<HTMLDivElement>(null);

  const toggleExpanded = useCallback((key: string) => {
    setExpandedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }, []);

  const toggleLevel = (level: string) => {
    setSelectedLevels((prev) => {
      const next = new Set(prev);
      if (next.has(level)) {
        next.delete(level);
      } else {
        next.add(level);
      }
      return next;
    });
    setVisibleCount(PAGE_SIZE);
  };

  const { data: entries, isLoading } = useQuery({
    queryKey: ['admin', 'logs'],
    queryFn: api.admin.getRecentLogs,
    refetchInterval: 5000,
  });

  // Show newest first
  const reversed = [...(entries ?? [])].reverse();

  const filtered = reversed.filter((e) => {
    if (selectedLevels.size > 0 && !selectedLevels.has(e.level)) return false;
    if (search) {
      const q = search.toLowerCase();
      if (
        !e.message.toLowerCase().includes(q) &&
        !Object.values(e.fields ?? {}).some((v) => v.toLowerCase().includes(q))
      )
        return false;
    }
    return true;
  });

  const visible = filtered.slice(0, visibleCount);
  const hasMore = visibleCount < filtered.length;

  // IntersectionObserver for infinite scroll
  const observerCallback = useCallback(
    (entries: IntersectionObserverEntry[]) => {
      if (entries[0]?.isIntersecting && hasMore) {
        setVisibleCount((prev) => prev + PAGE_SIZE);
      }
    },
    [hasMore],
  );

  useEffect(() => {
    const el = sentinelRef.current;
    if (!el) return;
    const observer = new IntersectionObserver(observerCallback, {
      rootMargin: '200px',
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [observerCallback]);

  if (isLoading) {
    return (
      <div className="animate-pulse space-y-3">
        {[...Array(5)].map((_, i) => (
          <div key={i} className="h-10 bg-gray-200 rounded" />
        ))}
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">Server Logs</h1>
        <span className="text-sm text-gray-500">
          {reversed.length} entries (auto-refreshes)
        </span>
      </div>

      {/* Filters */}
      <div className="flex items-center gap-3 mb-4">
        <div className="flex items-center gap-1">
          {ALL_LEVELS.map((level) => {
            const active = selectedLevels.has(level);
            const baseStyle = LEVEL_STYLES[level] ?? 'bg-gray-100 text-gray-800';
            return (
              <button
                key={level}
                onClick={() => toggleLevel(level)}
                className={`text-xs font-semibold px-2.5 py-1 rounded transition-opacity ${baseStyle} ${
                  active ? 'opacity-100' : 'opacity-30'
                }`}
              >
                {level.toUpperCase()}
              </button>
            );
          })}
        </div>
        <input
          type="text"
          placeholder="Search messages..."
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setVisibleCount(PAGE_SIZE);
          }}
          className="flex-1 border rounded px-3 py-2 text-sm"
        />
        <span className="text-sm text-gray-500">
          {filtered.length === reversed.length
            ? `${filtered.length} entries`
            : `${filtered.length} of ${reversed.length}`}
        </span>
      </div>

      {/* Log table */}
      {filtered.length === 0 ? (
        <div className="text-center py-12 text-gray-500">
          <p className="text-lg font-medium">No log entries</p>
          <p className="text-sm mt-1">
            {reversed.length === 0
              ? 'No warnings or errors have been logged since the server started.'
              : 'No entries match your filters.'}
          </p>
        </div>
      ) : (
      <>
        <div className="border rounded-lg overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-left">
              <tr>
                <th className="px-4 py-2 font-medium text-gray-600 w-28 sm:w-44">Time</th>
                <th className="px-4 py-2 font-medium text-gray-600 w-16 sm:w-20">Level</th>
                <th className="px-4 py-2 font-medium text-gray-600">Message</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {visible.map((entry) => {
                const key = stableKey(entry);
                return (
                  <LogRow
                    key={key}
                    entry={entry}
                    expanded={expandedKeys.has(key)}
                    onToggle={() => toggleExpanded(key)}
                  />
                );
              })}
            </tbody>
          </table>
        </div>
        {hasMore && (
          <div ref={sentinelRef} className="py-4 text-center text-sm text-gray-400">
            Loading more...
          </div>
        )}
      </>
      )}
    </div>
  );
}

function LogRow({ entry, expanded, onToggle }: { entry: LogEntry; expanded: boolean; onToggle: () => void }) {
  const hasFields = entry.fields && Object.keys(entry.fields).length > 0;
  const style = LEVEL_STYLES[entry.level] ?? 'bg-gray-100 text-gray-800';

  // Build a compact inline summary: message + key fields
  const fieldEntries = Object.entries(entry.fields ?? {});
  const inlineParts: string[] = [];
  if (entry.message) inlineParts.push(entry.message);
  for (const [k, v] of fieldEntries) {
    inlineParts.push(`${k}=${v}`);
  }
  const inlineText = inlineParts.join('  ');

  return (
    <>
      <tr
        className={`hover:bg-gray-50 ${hasFields ? 'cursor-pointer' : ''}`}
        onClick={() => hasFields && onToggle()}
      >
        <td className="px-4 py-2 text-gray-500 font-mono text-xs whitespace-nowrap">
          {formatTimestamp(entry.timestamp)}
        </td>
        <td className="px-4 py-2">
          <span className={`inline-block text-xs font-semibold px-2 py-0.5 rounded ${style}`}>
            {entry.level.toUpperCase()}
          </span>
        </td>
        <td className="px-4 py-2 font-mono text-xs truncate max-w-0" title={inlineText}>
          <span>{entry.message}</span>
          {fieldEntries.length > 0 && (
            <span className="text-gray-400 ml-2">
              {fieldEntries.map(([k, v]) => (
                <span key={k} className="mr-2">
                  <span className="text-gray-500">{k}</span>={v}
                </span>
              ))}
            </span>
          )}
        </td>
      </tr>
      {expanded && hasFields && (
        <tr className="bg-gray-50">
          <td colSpan={3} className="px-4 py-2">
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs font-mono">
              {Object.entries(entry.fields!).map(([k, v]) => (
                <div key={k} className="contents">
                  <span className="text-gray-500 font-semibold">{k}</span>
                  <span className="text-gray-700 break-all">{v}</span>
                </div>
              ))}
            </div>
          </td>
        </tr>
      )}
    </>
  );
}
