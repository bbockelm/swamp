'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, type SoftwarePackage, type Analysis } from '@/lib/api';
import { useRouter, useSearchParams } from 'next/navigation';
import { useState } from 'react';
import Link from 'next/link';
import { AnalysisStatus } from '@/components/AnalysisStatus';
import { Pagination, paginate } from '@/components/Pagination';
import { FindingsTable } from '@/components/FindingsTable';
import { GitBranchInput } from '@/components/GitBranchInput';
import { useResolvedParams } from '@/lib/useResolvedParams';

const ANALYSES_PAGE_SIZE = 10;

type Tab = 'packages' | 'analyses' | 'findings' | 'api-keys' | 'settings';

export default function ProjectDetailClient() {
  const { id } = useResolvedParams<{ id: string }>('/projects/[id]');
  const router = useRouter();
  const searchParams = useSearchParams();
  const queryClient = useQueryClient();
  const initialTab = (searchParams.get('tab') as Tab) || 'packages';
  const [tab, setTab] = useState<Tab>(initialTab);

  const { data: session } = useQuery({
    queryKey: ['session'],
    queryFn: api.auth.me,
  });

  const { data: project, isLoading } = useQuery({
    queryKey: ['project', id],
    queryFn: () => api.projects.get(id),
  });

  const { data: packages } = useQuery({
    queryKey: ['packages', id],
    queryFn: () => api.packages.list(id),
    enabled: tab === 'packages' || tab === 'findings',
  });

  const { data: analyses } = useQuery({
    queryKey: ['analyses', id],
    queryFn: () => api.analyses.list(id),
    enabled: tab === 'analyses',
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.projects.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['projects'] });
      router.push('/projects');
    },
  });

  if (isLoading) return <p>Loading...</p>;
  if (!project) return <p>Project not found.</p>;

  const canEdit = project.my_role === 'write' || project.my_role === 'admin';
  const isAdmin = project.my_role === 'admin';
  // System-level roles that can toggle uses_global_key
  const canEditGlobalKey = session?.roles?.includes('admin') || session?.roles?.includes('project_creator');

  const tabs: { key: Tab; label: string }[] = [
    { key: 'packages', label: 'Packages' },
    { key: 'analyses', label: 'Analyses' },
    { key: 'findings', label: 'Findings' },
    ...(isAdmin ? [{ key: 'api-keys' as Tab, label: 'API Keys' }] : []),
    ...(canEdit ? [{ key: 'settings' as Tab, label: 'Settings' }] : []),
  ];

  return (
    <div>
      <div className="mb-6">
        <h1 className="text-2xl font-bold">{project.name}</h1>
        {project.description && (
          <p className="text-gray-500 mt-1">{project.description}</p>
        )}
      </div>

      {/* Tabs */}
      <div className="border-b mb-6">
        <div className="flex gap-4">
          {tabs.map((t) => (
            <button
              key={t.key}
              onClick={() => setTab(t.key)}
              className={`pb-2 px-1 text-sm font-medium border-b-2 ${
                tab === t.key
                  ? 'border-blue-600 text-blue-600'
                  : 'border-transparent text-gray-500 hover:text-gray-700'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>

      {/* Packages tab */}
      {tab === 'packages' && (
        <PackagesTab projectId={id} packages={packages} canEdit={canEdit} />
      )}

      {/* Analyses tab */}
      {tab === 'analyses' && (
        <AnalysesTab projectId={id} analyses={analyses} packages={packages} canEdit={canEdit} />
      )}

      {/* Findings tab */}
      {tab === 'findings' && (
        <FindingsTab projectId={id} packages={packages} canEdit={canEdit} />
      )}

      {/* API Keys tab */}
      {tab === 'api-keys' && isAdmin && (
        <ProviderKeysTab projectId={id} />
      )}

      {/* Settings tab */}
      {tab === 'settings' && canEdit && (
        <SettingsTab
          project={project}
          canEditGlobalKey={canEditGlobalKey}
          onDelete={() => {
            if (confirm('Delete this project? This cannot be undone.')) {
              deleteMutation.mutate();
            }
          }}
        />
      )}
    </div>
  );
}

function PackagesTab({
  projectId,
  packages,
  canEdit,
}: {
  projectId: string;
  packages?: SoftwarePackage[];
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [gitUrl, setGitUrl] = useState('');
  const [gitBranch, setGitBranch] = useState('main');
  const [analysisPrompt, setAnalysisPrompt] = useState('');

  const createMutation = useMutation({
    mutationFn: () =>
      api.packages.create(projectId, {
        name,
        git_url: gitUrl,
        git_branch: gitBranch,
        analysis_prompt: analysisPrompt,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['packages', projectId] });
      setAdding(false);
      setName('');
      setGitUrl('');
      setGitBranch('main');
      setAnalysisPrompt('');
    },
  });

  const updateMutation = useMutation({
    mutationFn: (pkgId: string) =>
      api.packages.update(projectId, pkgId, {
        name,
        git_url: gitUrl,
        git_branch: gitBranch,
        analysis_prompt: analysisPrompt,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['packages', projectId] });
      setEditingId(null);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (pkgId: string) => api.packages.delete(projectId, pkgId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['packages', projectId] });
    },
  });

  const startEdit = (pkg: SoftwarePackage) => {
    setEditingId(pkg.id);
    setName(pkg.name);
    setGitUrl(pkg.git_url);
    setGitBranch(pkg.git_branch);
    setAnalysisPrompt(pkg.analysis_prompt || '');
    setAdding(false);
  };

  const cancelEdit = () => {
    setEditingId(null);
    setName('');
    setGitUrl('');
    setGitBranch('main');
    setAnalysisPrompt('');
  };

  return (
    <div>
      <div className="flex justify-between items-center mb-4">
        <h2 className="text-lg font-semibold">Software Packages</h2>
        {canEdit && (
          <button
            onClick={() => { setAdding(true); setEditingId(null); setName(''); setGitUrl(''); setGitBranch('main'); setAnalysisPrompt(''); }}
            className="bg-blue-600 text-white px-3 py-1.5 text-sm rounded hover:bg-blue-700"
          >
            Add Package
          </button>
        )}
      </div>

      {adding && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            createMutation.mutate();
          }}
          className="bg-gray-50 p-4 rounded border mb-4 space-y-3"
        >
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Git URL
            </label>
            <input
              type="url"
              value={gitUrl}
              onChange={(e) => setGitUrl(e.target.value)}
              required
              autoFocus
              className="w-full border rounded px-3 py-2 text-sm"
              placeholder="https://github.com/org/repo.git"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Name
              </label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                className="w-full border rounded px-3 py-2 text-sm"
              />
            </div>
            <GitBranchInput
              gitUrl={gitUrl}
              value={gitBranch}
              onChange={setGitBranch}
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Custom Analysis Prompt
            </label>
            <textarea
              value={analysisPrompt}
              onChange={(e) => setAnalysisPrompt(e.target.value)}
              rows={2}
              className="w-full border rounded px-3 py-2 text-sm"
              placeholder="Focus on authentication and SQL injection..."
            />
          </div>
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={createMutation.isPending}
              className="bg-blue-600 text-white px-3 py-1.5 text-sm rounded hover:bg-blue-700 disabled:opacity-50"
            >
              {createMutation.isPending ? 'Adding...' : 'Add'}
            </button>
            <button
              type="button"
              onClick={() => setAdding(false)}
              className="px-3 py-1.5 text-sm border rounded hover:bg-gray-100"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {!packages?.length ? (
        <p className="text-gray-500 text-sm">
          {canEdit ? 'No packages yet. Add a Git repository to analyze.' : 'No packages configured for this project.'}
        </p>
      ) : (
        <div className="space-y-3">
          {packages.map((pkg) =>
            editingId === pkg.id ? (
              <form
                key={pkg.id}
                onSubmit={(e) => {
                  e.preventDefault();
                  updateMutation.mutate(pkg.id);
                }}
                className="bg-gray-50 p-4 rounded border space-y-3"
              >
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">
                    Git URL
                  </label>
                  <input
                    type="url"
                    value={gitUrl}
                    onChange={(e) => setGitUrl(e.target.value)}
                    required
                    className="w-full border rounded px-3 py-2 text-sm"
                  />
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-1">
                      Name
                    </label>
                    <input
                      type="text"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      required
                      className="w-full border rounded px-3 py-2 text-sm"
                    />
                  </div>
                  <GitBranchInput
                    gitUrl={gitUrl}
                    value={gitBranch}
                    onChange={setGitBranch}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">
                    Custom Analysis Prompt
                  </label>
                  <textarea
                    value={analysisPrompt}
                    onChange={(e) => setAnalysisPrompt(e.target.value)}
                    rows={2}
                    className="w-full border rounded px-3 py-2 text-sm"
                  />
                </div>
                <div className="flex gap-2">
                  <button
                    type="submit"
                    disabled={updateMutation.isPending}
                    className="bg-blue-600 text-white px-3 py-1.5 text-sm rounded hover:bg-blue-700 disabled:opacity-50"
                  >
                    {updateMutation.isPending ? 'Saving...' : 'Save'}
                  </button>
                  <button
                    type="button"
                    onClick={cancelEdit}
                    className="px-3 py-1.5 text-sm border rounded hover:bg-gray-100"
                  >
                    Cancel
                  </button>
                </div>
              </form>
            ) : (
              <div
                key={pkg.id}
                className="p-4 bg-white border rounded flex justify-between items-start"
              >
                <div>
                  <h3 className="font-medium">{pkg.name}</h3>
                  <p className="text-sm text-gray-500 font-mono">{pkg.git_url}</p>
                  <p className="text-xs text-gray-400">
                    Branch: {pkg.git_branch}
                    {pkg.git_commit && ` · ${pkg.git_commit.slice(0, 8)}`}
                  </p>
                  {pkg.analysis_prompt && (
                    <p className="text-xs text-gray-400 mt-1 italic">
                      Prompt: {pkg.analysis_prompt.length > 80 ? pkg.analysis_prompt.slice(0, 80) + '…' : pkg.analysis_prompt}
                    </p>
                  )}
                </div>
                <div className="flex gap-2">
                  {canEdit && (
                    <>
                      <button
                        onClick={() => startEdit(pkg)}
                        className="text-blue-500 text-sm hover:text-blue-700"
                      >
                        Edit
                      </button>
                      <button
                        onClick={() => {
                          if (confirm('Delete this package?')) {
                            deleteMutation.mutate(pkg.id);
                          }
                        }}
                        className="text-red-500 text-sm hover:text-red-700"
                      >
                        Delete
                      </button>
                    </>
                  )}
                </div>
              </div>
            )
          )}
        </div>
      )}
    </div>
  );
}

function AnalysesTab({
  projectId,
  analyses,
  packages,
  canEdit,
}: {
  projectId: string;
  analyses?: Analysis[];
  packages?: SoftwarePackage[];
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const [selectedPkgs, setSelectedPkgs] = useState<string[]>([]);
  const [customPrompt, setCustomPrompt] = useState('');
  const [analysisPage, setAnalysisPage] = useState(1);

  const { data: agentStatus } = useQuery({
    queryKey: ['agent-status'],
    queryFn: () => api.agent.status(),
    staleTime: 60_000,
  });

  const triggerMutation = useMutation({
    mutationFn: () =>
      api.analyses.create(projectId, {
        package_ids: selectedPkgs,
        custom_prompt: customPrompt || undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['analyses', projectId] });
      setSelectedPkgs([]);
      setCustomPrompt('');
    },
  });

  return (
    <div>
      {/* Trigger new analysis */}
      {canEdit && packages && packages.length > 0 && (
        <div className="bg-gray-50 p-4 rounded border mb-6">
          <h3 className="text-sm font-medium mb-2">Run New Analysis</h3>
          {agentStatus && !agentStatus.ready && (
            <div className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded p-2 mb-3">
              Analysis agent is not configured. Set <code className="bg-amber-100 px-1 rounded">AGENT_API_KEY</code> or <code className="bg-amber-100 px-1 rounded">AGENT_API_KEY_FILE</code> to enable.
            </div>
          )}
          {triggerMutation.isError && (
            <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded p-2 mb-3">
              {triggerMutation.error?.message || 'Failed to start analysis'}
            </div>
          )}
          <div className="space-y-1 mb-3">
            {packages.map((pkg) => (
              <label
                key={pkg.id}
                className="flex items-center gap-2 text-sm"
              >
                <input
                  type="checkbox"
                  checked={selectedPkgs.includes(pkg.id)}
                  onChange={(e) => {
                    setSelectedPkgs((prev) =>
                      e.target.checked
                        ? [...prev, pkg.id]
                        : prev.filter((x) => x !== pkg.id)
                    );
                  }}
                />
                {pkg.name} ({pkg.git_branch})
              </label>
            ))}
          </div>
          <div className="mb-3">
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Additional Prompt
            </label>
            <textarea
              value={customPrompt}
              onChange={(e) => setCustomPrompt(e.target.value)}
              rows={2}
              className="w-full border rounded px-3 py-2 text-sm"
              placeholder="Focus on specific areas, e.g. 'Pay special attention to the OAuth flow and token handling...'"
            />
          </div>
          <button
            onClick={() => triggerMutation.mutate()}
            disabled={!selectedPkgs.length || triggerMutation.isPending || !agentStatus?.ready}
            className="bg-green-600 text-white px-3 py-1.5 text-sm rounded hover:bg-green-700 disabled:opacity-50"
          >
            {triggerMutation.isPending ? 'Starting...' : 'Start Analysis'}
          </button>
        </div>
      )}

      <h2 className="text-lg font-semibold mb-4">Analysis History</h2>
      {!analyses?.length ? (
        <p className="text-gray-500 text-sm">No analyses yet.</p>
      ) : (
        <>
          <div className="space-y-3">
            {paginate(analyses, analysisPage, ANALYSES_PAGE_SIZE).map((a) => (
              <Link
                key={a.id}
                href={`/projects/${projectId}/analyses/${a.id}`}
                className="block p-4 bg-white border rounded hover:shadow-md transition"
              >
                <div className="flex justify-between items-center">
                  <div>
                    <span className="font-mono text-sm text-gray-600">
                      {a.id.slice(0, 8)}
                    </span>
                    <AnalysisStatus status={a.status} className="ml-2" />
                  </div>
                  <span className="text-xs text-gray-400">
                    {new Date(a.created_at).toLocaleString()}
                  </span>
                </div>
                {a.git_commit && (
                  <span className="text-xs font-mono text-gray-400 mt-1">
                    Commit: {a.git_commit.slice(0, 12)}
                  </span>
                )}
                {a.status_detail && (
                  <p className="text-sm text-gray-500 mt-1">
                    {a.status_detail}
                  </p>
                )}
              </Link>
            ))}
          </div>
          <Pagination
            currentPage={analysisPage}
            totalPages={Math.ceil(analyses.length / ANALYSES_PAGE_SIZE)}
            onPageChange={setAnalysisPage}
          />
        </>
      )}
    </div>
  );
}

function FindingsTab({
  projectId,
  packages,
  canEdit,
}: {
  projectId: string;
  packages?: SoftwarePackage[];
  canEdit: boolean;
}) {
  const searchParams = useSearchParams();
  const initialAnalysisId = searchParams.get('analysis') || undefined;
  const initialFindingId = searchParams.get('finding') || undefined;
  // Get the first package's git URL for GitHub linking.
  const gitUrl = packages?.[0]?.git_url;

  return (
    <div>
      <h2 className="text-lg font-semibold mb-4">Security Findings</h2>
      <FindingsTable
        projectId={projectId}
        gitUrl={gitUrl}
        initialAnalysisId={initialAnalysisId}
        initialFindingId={initialFindingId}
        canEdit={canEdit}
      />
    </div>
  );
}

function SettingsTab({
  project,
  canEditGlobalKey,
  onDelete,
}: {
  project: { id: string; name: string; description: string; uses_global_key: boolean };
  canEditGlobalKey?: boolean;
  onDelete: () => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description);
  const [usesGlobalKey, setUsesGlobalKey] = useState(project.uses_global_key);

  const updateMutation = useMutation({
    mutationFn: () =>
      api.projects.update(project.id, { name, description, uses_global_key: usesGlobalKey }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['project', project.id] });
      queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });

  return (
    <div className="max-w-xl space-y-6">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          updateMutation.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Name
          </label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
            className="w-full border rounded px-3 py-2"
          />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Description
          </label>
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={3}
            className="w-full border rounded px-3 py-2"
          />
        </div>

        {/* Global Key toggle - only shown to admins/project_creators */}
        {canEditGlobalKey && (
          <div className="border-t pt-4">
            <label className="flex items-center gap-3">
              <input
                type="checkbox"
                checked={usesGlobalKey}
                onChange={(e) => setUsesGlobalKey(e.target.checked)}
                className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
              />
              <span className="text-sm font-medium text-gray-700">
                Use global agent API key
              </span>
            </label>
            <p className="text-xs text-gray-500 mt-1 ml-7">
              When enabled, this project can use the system&apos;s shared Anthropic API key
              instead of requiring a project-specific key.
            </p>
          </div>
        )}

        <button
          type="submit"
          disabled={updateMutation.isPending}
          className="bg-blue-600 text-white px-4 py-2 rounded hover:bg-blue-700 disabled:opacity-50"
        >
          {updateMutation.isPending ? 'Saving...' : 'Save Changes'}
        </button>
        {updateMutation.isSuccess && (
          <span className="text-green-600 text-sm ml-3">Saved!</span>
        )}
      </form>

      <div className="border-t pt-6">
        <h3 className="text-lg font-semibold text-red-600 mb-2">
          Danger Zone
        </h3>
        <p className="text-sm text-gray-600 mb-3">
          Deleting the project removes all packages, analyses, and results.
        </p>
        <button
          onClick={onDelete}
          className="bg-red-600 text-white px-4 py-2 rounded hover:bg-red-700"
        >
          Delete Project
        </button>
      </div>
    </div>
  );
}

function ProviderKeysTab({ projectId }: { projectId: string }) {
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [provider, setProvider] = useState('anthropic');
  const [label, setLabel] = useState('');
  const [apiKey, setApiKey] = useState('');

  const { data: keys, isLoading } = useQuery({
    queryKey: ['provider-keys', projectId],
    queryFn: () => api.providerKeys.list(projectId),
  });

  const createMutation = useMutation({
    mutationFn: () =>
      api.providerKeys.create(projectId, {
        provider,
        label,
        api_key: apiKey,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-keys', projectId] });
      setAdding(false);
      setLabel('');
      setApiKey('');
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (keyId: string) => api.providerKeys.revoke(projectId, keyId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-keys', projectId] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (keyId: string) => api.providerKeys.delete(projectId, keyId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-keys', projectId] });
    },
  });

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-lg font-semibold">Provider API Keys</h2>
          <p className="text-sm text-gray-500">
            API keys are encrypted at rest. Only the last 4 characters are ever displayed.
          </p>
        </div>
        {!adding && (
          <button
            onClick={() => setAdding(true)}
            className="bg-blue-600 text-white px-4 py-2 rounded hover:bg-blue-700 text-sm"
          >
            Add Key
          </button>
        )}
      </div>

      {adding && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            createMutation.mutate();
          }}
          className="border rounded p-4 mb-4 space-y-3 bg-gray-50"
        >
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Provider
            </label>
            <select
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
              className="w-full border rounded px-3 py-2"
            >
              <option value="anthropic">Anthropic</option>
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Label
            </label>
            <input
              type="text"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="e.g. Production key"
              className="w-full border rounded px-3 py-2"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              API Key
            </label>
            <input
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              required
              placeholder="sk-ant-..."
              className="w-full border rounded px-3 py-2 font-mono"
            />
          </div>
          {createMutation.isError && (
            <p className="text-red-600 text-sm">
              {createMutation.error?.message || 'An unexpected error occurred'}
            </p>
          )}
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={createMutation.isPending}
              className="bg-blue-600 text-white px-4 py-2 rounded hover:bg-blue-700 disabled:opacity-50 text-sm"
            >
              {createMutation.isPending ? 'Saving...' : 'Save Key'}
            </button>
            <button
              type="button"
              onClick={() => {
                setAdding(false);
                setApiKey('');
              }}
              className="border px-4 py-2 rounded text-sm"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {isLoading ? (
        <p className="text-sm text-gray-500">Loading...</p>
      ) : !keys?.length ? (
        <p className="text-sm text-gray-500">No provider keys configured.</p>
      ) : (
        <div className="border rounded divide-y">
          {keys.map((k) => (
            <div
              key={k.id}
              className={`flex items-center justify-between px-4 py-3 ${
                !k.is_active ? 'opacity-50' : ''
              }`}
            >
              <div>
                <div className="flex items-center gap-2">
                  <span className="text-xs font-semibold uppercase bg-gray-100 text-gray-700 px-2 py-0.5 rounded">
                    {k.provider}
                  </span>
                  <span className="font-medium">{k.label || 'Unnamed'}</span>
                  <code className="text-xs text-gray-500">{k.key_hint}</code>
                </div>
                <div className="text-xs text-gray-500 mt-1">
                  Added {new Date(k.created_at).toLocaleDateString()}
                  {k.revoked_at && (
                    <span className="text-red-500 ml-2">
                      Revoked {new Date(k.revoked_at).toLocaleDateString()}
                    </span>
                  )}
                </div>
              </div>
              {k.is_active && (
                <div className="flex gap-2">
                  <button
                    onClick={() => {
                      if (confirm('Revoke this key? It will no longer be usable.')) {
                        revokeMutation.mutate(k.id);
                      }
                    }}
                    className="text-yellow-600 hover:text-yellow-800 text-sm"
                  >
                    Revoke
                  </button>
                  <button
                    onClick={() => {
                      if (confirm('Permanently delete this key?')) {
                        deleteMutation.mutate(k.id);
                      }
                    }}
                    className="text-red-600 hover:text-red-800 text-sm"
                  >
                    Delete
                  </button>
                </div>
              )}
              {!k.is_active && (
                <button
                  onClick={() => {
                    if (confirm('Permanently delete this revoked key?')) {
                      deleteMutation.mutate(k.id);
                    }
                  }}
                  className="text-red-600 hover:text-red-800 text-sm"
                >
                  Delete
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
