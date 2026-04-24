'use client';

import { useEffect, useState } from 'react';
import { api } from '@/lib/api';

export function MarkdownReport({
  projectId,
  analysisId,
  resultId,
}: {
  projectId: string;
  analysisId: string;
  resultId: string;
}) {
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    const url = api.analyses.downloadResult(projectId, analysisId, resultId);
    fetch(url, { credentials: 'include' })
      .then((r) => {
        if (!r.ok) throw new Error('Failed to load report');
        return r.text();
      })
      .then((text) => {
        setContent(text);
        setLoading(false);
      })
      .catch((err) => {
        setError(err.message);
        setLoading(false);
      });
  }, [projectId, analysisId, resultId]);

  if (loading) return <p className="text-sm text-gray-500">Loading report...</p>;
  if (error) return <p className="text-sm text-red-600">{error}</p>;

  return (
    <div className="prose prose-sm max-w-none bg-white border rounded p-6">
      <RenderedMarkdown content={content} />
    </div>
  );
}

export function RenderedMarkdown({
  content,
  imageBasePath = '',
}: {
  content: string;
  imageBasePath?: string;
}) {
  // Simple markdown rendering without extra dependencies.
  // Supports headers (h1–h5), bold, italic, code blocks, lists, links,
  // horizontal rules, and pipe-delimited tables.
  const lines = content.split('\n');
  const elements: React.ReactNode[] = [];
  let inCodeBlock = false;
  let codeLines: string[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // --- fenced code blocks ---
    if (line.startsWith('```')) {
      if (inCodeBlock) {
        elements.push(
          <pre key={i} className="bg-gray-900 text-green-400 p-4 rounded overflow-x-auto text-xs">
            <code>{codeLines.join('\n')}</code>
          </pre>
        );
        codeLines = [];
        inCodeBlock = false;
      } else {
        inCodeBlock = true;
      }
      i++;
      continue;
    }

    if (inCodeBlock) {
      codeLines.push(line);
      i++;
      continue;
    }

    // --- markdown images ---
    const imageLine = line.trim().match(/^!\[([^\]]*)\]\(([^)]+)\)$/);
    if (imageLine) {
      const alt = imageLine[1];
      const src = resolveImageSource(imageLine[2], imageBasePath);
      elements.push(
        <figure key={i} className="my-6">
          <img
            src={src}
            alt={alt}
            className="w-full h-auto rounded-lg border border-gray-200 shadow-sm"
            loading="lazy"
          />
          {alt && <figcaption className="mt-2 text-center text-xs text-gray-500">{alt}</figcaption>}
        </figure>
      );
      i++;
      continue;
    }

    // --- blockquotes ---
    if (line.trimStart().startsWith('>')) {
      const quoteLines: string[] = [];
      while (i < lines.length && lines[i].trimStart().startsWith('>')) {
        quoteLines.push(lines[i].trimStart().replace(/^>\s?/, ''));
        i++;
      }
      // Preserve internal paragraph breaks; render as stacked paragraphs.
      const paragraphs: string[][] = [[]];
      for (const ql of quoteLines) {
        if (ql.trim() === '') {
          paragraphs.push([]);
        } else {
          paragraphs[paragraphs.length - 1].push(ql);
        }
      }
      elements.push(
        <blockquote
          key={`q-${i}`}
          className="my-4 border-l-4 border-brand-200 bg-brand-50/40 pl-4 pr-3 py-2 text-gray-700"
        >
          {paragraphs
            .filter((p) => p.length > 0)
            .map((p, pi) => (
              <p key={pi} className={pi === 0 ? 'text-[0.95rem]' : 'text-[0.95rem] mt-2'}>
                {formatInline(p.join(' '))}
              </p>
            ))}
        </blockquote>
      );
      continue;
    }

    // --- pipe tables ---
    if (line.includes('|') && line.trim().startsWith('|')) {
      const tableRows: string[][] = [];
      let hasHeader = false;
      let j = i;

      // Collect consecutive pipe-delimited lines.
      while (j < lines.length && lines[j].trim().startsWith('|')) {
        const row = lines[j]
          .trim()
          .replace(/^\|/, '')
          .replace(/\|$/, '')
          .split('|')
          .map((c) => c.trim());

        // Detect separator row (e.g. |---|---|)
        if (row.every((c) => /^[-:]+$/.test(c))) {
          hasHeader = true;
          j++;
          continue;
        }
        tableRows.push(row);
        j++;
      }

      if (tableRows.length > 0) {
        const headerRow = hasHeader ? tableRows[0] : undefined;
        const bodyRows = hasHeader ? tableRows.slice(1) : tableRows;
        elements.push(
          <div key={i} className="overflow-x-auto my-3">
            <table className="min-w-full text-sm border border-gray-200">
              {headerRow && (
                <thead>
                  <tr className="bg-gray-100">
                    {headerRow.map((cell, ci) => (
                      <th key={ci} className="px-3 py-1.5 text-left font-semibold border-b border-gray-200 text-xs">
                        {formatInline(cell)}
                      </th>
                    ))}
                  </tr>
                </thead>
              )}
              <tbody>
                {bodyRows.map((row, ri) => (
                  <tr key={ri} className={ri % 2 === 0 ? 'bg-white' : 'bg-gray-50'}>
                    {row.map((cell, ci) => (
                      <td key={ci} className="px-3 py-1.5 border-b border-gray-100 text-xs">
                        {formatInline(cell)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      }

      i = j;
      continue;
    }

    // --- horizontal rule ---
    if (/^[-*_]{3,}\s*$/.test(line.trim())) {
      elements.push(<hr key={i} className="my-4 border-gray-300" />);
      i++;
      continue;
    }

    // --- headers ---
    if (line.startsWith('##### ')) {
      elements.push(
        <h5 key={i} className="text-sm font-semibold mt-3 mb-1">
          {formatInline(line.slice(6))}
        </h5>
      );
    } else if (line.startsWith('#### ')) {
      elements.push(
        <h4 key={i} className="text-base font-semibold mt-3 mb-1">
          {formatInline(line.slice(5))}
        </h4>
      );
    } else if (line.startsWith('### ')) {
      elements.push(
        <h3 key={i} className="text-lg font-semibold mt-4 mb-2">
          {formatInline(line.slice(4))}
        </h3>
      );
    } else if (line.startsWith('## ')) {
      elements.push(
        <h2 key={i} className="text-xl font-bold mt-6 mb-2">
          {formatInline(line.slice(3))}
        </h2>
      );
    } else if (line.startsWith('# ')) {
      elements.push(
        <h1 key={i} className="text-2xl font-bold mt-6 mb-3">
          {formatInline(line.slice(2))}
        </h1>
      );
    } else if (line.startsWith('- ') || line.startsWith('* ')) {
      // Collect consecutive unordered list items into a <ul>.
      const items: { key: number; text: string }[] = [];
      while (i < lines.length && (lines[i].startsWith('- ') || lines[i].startsWith('* '))) {
        items.push({ key: i, text: lines[i].slice(2) });
        i++;
      }
      elements.push(
        <ul key={items[0].key} className="ml-6 list-disc my-3 space-y-1">
          {items.map((item) => (
            <li key={item.key}>{formatInline(item.text)}</li>
          ))}
        </ul>
      );
      continue; // skip the i++ at the end
    } else if (line.match(/^\d+\. /)) {
      // Collect consecutive ordered list items into an <ol> with correct start.
      const items: { key: number; text: string }[] = [];
      const startNum = parseInt(line.match(/^(\d+)\. /)![1], 10);
      while (i < lines.length && lines[i].match(/^\d+\. /)) {
        items.push({ key: i, text: lines[i].replace(/^\d+\. /, '') });
        i++;
      }
      elements.push(
        <ol key={items[0].key} start={startNum} className="ml-6 list-decimal my-3 space-y-1">
          {items.map((item) => (
            <li key={item.key}>{formatInline(item.text)}</li>
          ))}
        </ol>
      );
      continue; // skip the i++ at the end
    } else if (line.trim() === '') {
      // Blank lines act as paragraph separators; no explicit element needed.
    } else {
      // Coalesce consecutive non-blank, non-structural lines into one paragraph.
      const paraLines: string[] = [line];
      const startIdx = i;
      let j = i + 1;
      while (j < lines.length) {
        const next = lines[j];
        if (next.trim() === '') break;
        if (next.startsWith('```')) break;
        if (next.startsWith('#')) break;
        if (next.startsWith('- ') || next.startsWith('* ')) break;
        if (next.match(/^\d+\. /)) break;
        if (next.trimStart().startsWith('>')) break;
        if (next.trim().startsWith('|')) break;
        if (/^[-*_]{3,}\s*$/.test(next.trim())) break;
        if (/^!\[([^\]]*)\]\(([^)]+)\)$/.test(next.trim())) break;
        paraLines.push(next);
        j++;
      }
      elements.push(
        <p key={startIdx} className="my-3 leading-relaxed">
          {formatInline(paraLines.join(' '))}
        </p>
      );
      i = j;
      continue;
    }

    i++;
  }

  return <>{elements}</>;
}

function resolveImageSource(rawSrc: string, imageBasePath: string): string {
  if (!rawSrc) {
    return rawSrc;
  }
  if (rawSrc.startsWith('http://') || rawSrc.startsWith('https://') || rawSrc.startsWith('/')) {
    return rawSrc;
  }
  if (!imageBasePath) {
    return rawSrc;
  }
  const cleanBase = imageBasePath.endsWith('/') ? imageBasePath : `${imageBasePath}/`;
  const cleanSrc = rawSrc.replace(/^\.\//, '').replace(/^images\//, '');
  return `${cleanBase}${cleanSrc}`;
}

function formatInline(text: string): React.ReactNode {
  // Handle inline code, bold, italic, and links.
  // At each step, find the earliest matching pattern so that e.g. bold
  // markers before a backtick are not swallowed by the code regex.
  const parts: React.ReactNode[] = [];
  let remaining = text;
  let key = 0;

  while (remaining.length > 0) {
    // Find the earliest match among the patterns we support.
    const codeIdx = remaining.indexOf('`');
    const boldIdx = remaining.indexOf('**');
    const linkIdx = remaining.indexOf('[');

    // Pick whichever comes first (ignoring -1 = not found).
    const candidates: [number, string][] = [];
    if (codeIdx >= 0) candidates.push([codeIdx, 'code']);
    if (boldIdx >= 0) candidates.push([boldIdx, 'bold']);
    if (linkIdx >= 0) candidates.push([linkIdx, 'link']);
    candidates.sort((a, b) => a[0] - b[0]);

    let matched = false;
    for (const [, kind] of candidates) {
      if (kind === 'code') {
        const m = remaining.match(/^(.*?)`([^`]+)`([\s\S]*)$/);
        if (m) {
          if (m[1]) parts.push(m[1]);
          parts.push(
            <code key={key++} className="bg-gray-100 px-1 py-0.5 rounded text-xs font-mono">
              {m[2]}
            </code>
          );
          remaining = m[3];
          matched = true;
          break;
        }
      } else if (kind === 'bold') {
        const m = remaining.match(/^(.*?)\*\*(.+?)\*\*([\s\S]*)$/);
        if (m) {
          if (m[1]) parts.push(m[1]);
          parts.push(<strong key={key++}>{formatInline(m[2])}</strong>);
          remaining = m[3];
          matched = true;
          break;
        }
      } else if (kind === 'link') {
        const m = remaining.match(/^(.*?)\[([^\]]+)\]\(([^)]+)\)([\s\S]*)$/);
        if (m) {
          if (m[1]) parts.push(m[1]);
          parts.push(
            <a key={key++} href={m[3]} className="text-brand-600 hover:underline" target="_blank" rel="noopener noreferrer">
              {m[2]}
            </a>
          );
          remaining = m[4];
          matched = true;
          break;
        }
      }
    }

    if (!matched) {
      parts.push(remaining);
      break;
    }
  }

  return parts.length === 1 ? parts[0] : <>{parts}</>;
}
