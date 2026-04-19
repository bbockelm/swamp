'use client';

import { useState, useEffect, useRef, useCallback } from 'react';
import { api } from '@/lib/api';

interface GitHubRepo {
  default_branch: string;
}

interface GitHubBranch {
  name: string;
}

/** Parse "owner/repo" from a GitHub URL, or return null. */
function parseGitHub(url: string): { owner: string; repo: string } | null {
  if (!url) return null;
  // Match https://github.com/owner/repo(.git)
  const m = url.match(/github\.com[/:]([^/]+)\/([^/.]+?)(?:\.git)?\/?$/i);
  return m ? { owner: m[1], repo: m[2] } : null;
}

interface Props {
  gitUrl: string;
  value: string;
  onChange: (branch: string) => void;
  /** Label CSS class override */
  labelClassName?: string;
  /** When provided, uses the backend branch API (supports private repos). */
  projectId?: string;
  packageId?: string;
}

export function GitBranchInput({ gitUrl, value, onChange, labelClassName, projectId, packageId }: Props) {
  const [branches, setBranches] = useState<string[]>([]);
  const [defaultBranch, setDefaultBranch] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [isGitHub, setIsGitHub] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const prevUrlRef = useRef<string>('');

  // Close dropdown when clicking outside
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, []);

  // Try to fetch branches from the backend (supports private repos via installation tokens).
  const fetchBranchesFromBackend = useCallback(async () => {
    if (!projectId || !packageId) return false;
    try {
      const names = await api.packages.listBranches(projectId, packageId);
      if (names && names.length > 0) {
        setBranches(names);
        // Use the first branch as the "default" heuristic (often main/master).
        setDefaultBranch(names[0]);
        if (!value || value === 'main' || value === 'master') {
          onChange(names[0]);
        }
        return true;
      }
    } catch {
      // Fall through to public API.
    }
    return false;
  }, [projectId, packageId, value, onChange]);

  // Fetch branches when gitUrl changes to a valid GitHub URL
  const fetchBranches = useCallback(async (owner: string, repo: string) => {
    setLoading(true);
    try {
      // Try backend API first for private repo support.
      const fromBackend = await fetchBranchesFromBackend();
      if (fromBackend) return;

      // Fall back to public GitHub API.
      const repoResp = await fetch(`https://api.github.com/repos/${owner}/${repo}`, {
        headers: { 'Accept': 'application/vnd.github.v3+json' },
      });
      if (!repoResp.ok) throw new Error('repo fetch failed');
      const repoData: GitHubRepo = await repoResp.json();
      setDefaultBranch(repoData.default_branch);

      // Fetch branches (up to 100)
      const branchResp = await fetch(
        `https://api.github.com/repos/${owner}/${repo}/branches?per_page=100`,
        { headers: { 'Accept': 'application/vnd.github.v3+json' } },
      );
      if (!branchResp.ok) throw new Error('branch fetch failed');
      const branchData: GitHubBranch[] = await branchResp.json();
      const names = branchData.map((b) => b.name);

      // Put default branch first
      names.sort((a, b) => {
        if (a === repoData.default_branch) return -1;
        if (b === repoData.default_branch) return 1;
        return a.localeCompare(b);
      });

      setBranches(names);

      // Auto-set to default branch if currently set to generic "main" or empty
      if (!value || value === 'main' || value === 'master') {
        onChange(repoData.default_branch);
      }
    } catch {
      // Not a public repo or rate limited — fall back to text input
      setBranches([]);
      setDefaultBranch(null);
    } finally {
      setLoading(false);
    }
  }, [value, onChange, fetchBranchesFromBackend]);

  useEffect(() => {
    const gh = parseGitHub(gitUrl);
    setIsGitHub(!!gh);

    if (!gh) {
      setBranches([]);
      setDefaultBranch(null);
      return;
    }

    const key = `${gh.owner}/${gh.repo}`;
    if (key === prevUrlRef.current) return;
    prevUrlRef.current = key;

    fetchBranches(gh.owner, gh.repo);
  }, [gitUrl, fetchBranches]);

  const filtered = branches.filter((b) =>
    b.toLowerCase().includes(search.toLowerCase())
  );

  // If we have GitHub branches, show the searchable combo
  if (isGitHub && branches.length > 0) {
    return (
      <div ref={wrapperRef} className="relative">
        <label className={labelClassName || "block text-sm font-medium text-gray-700 mb-1"}>
          Branch
          {defaultBranch && (
            <span className="text-xs font-normal text-gray-400 ml-1">(default: {defaultBranch})</span>
          )}
        </label>
        <button
          type="button"
          onClick={() => { setOpen(!open); setSearch(''); }}
          className="w-full border rounded px-3 py-2 text-sm text-left flex items-center justify-between bg-white hover:bg-gray-50"
        >
          <span className={value ? '' : 'text-gray-400'}>
            {value || 'Select branch...'}
            {value === defaultBranch && <span className="text-xs text-gray-400 ml-1">(default)</span>}
          </span>
          <svg className={`w-4 h-4 text-gray-400 transition-transform ${open ? 'rotate-180' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
          </svg>
        </button>
        {open && (
          <div className="absolute z-10 mt-1 w-full bg-white border rounded shadow-lg max-h-60 overflow-hidden">
            <div className="p-2 border-b">
              <input
                ref={inputRef}
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search branches..."
                className="w-full border rounded px-2 py-1.5 text-sm"
                autoFocus
              />
            </div>
            <div className="overflow-y-auto max-h-48">
              {filtered.length === 0 ? (
                <div className="px-3 py-2 text-sm text-gray-400">No matching branches</div>
              ) : (
                filtered.map((b) => (
                  <button
                    key={b}
                    type="button"
                    onClick={() => { onChange(b); setOpen(false); }}
                    className={`w-full text-left px-3 py-1.5 text-sm hover:bg-blue-50 flex items-center justify-between ${
                      b === value ? 'bg-blue-50 font-medium' : ''
                    }`}
                  >
                    <span>{b}</span>
                    {b === defaultBranch && <span className="text-xs text-gray-400">default</span>}
                  </button>
                ))
              )}
            </div>
          </div>
        )}
      </div>
    );
  }

  // Fallback: plain editable text input (non-GitHub or loading)
  return (
    <div>
      <label className={labelClassName || "block text-sm font-medium text-gray-700 mb-1"}>
        Branch
        {loading && <span className="text-xs font-normal text-gray-400 ml-1">(detecting...)</span>}
      </label>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full border rounded px-3 py-2 text-sm"
        placeholder="main"
      />
    </div>
  );
}
