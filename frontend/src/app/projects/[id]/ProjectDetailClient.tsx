'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, type SoftwarePackage, type Analysis, type Group, type Project } from '@/lib/api';
import { useRouter, useSearchParams } from 'next/navigation';
import { useState, useMemo, useCallback, useEffect, useRef } from 'react';
import Link from 'next/link';
import { AnalysisStatus } from '@/components/AnalysisStatus';
import { Pagination, paginate } from '@/components/Pagination';
import { FindingsTable } from '@/components/FindingsTable';
import { GitBranchInput, type GitBranchInputHandle } from '@/components/GitBranchInput';
import { ProjectGitHubTab } from '@/components/ProjectGitHubTab';
import { ProjectNRPTab } from '@/components/ProjectNRPTab';
import { useResolvedParams } from '@/lib/useResolvedParams';

const ANALYSES_PAGE_SIZE = 10;

type Tab = 'packages' | 'analyses' | 'findings' | 'api-keys' | 'github' | 'nrp' | 'settings';

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

  const { data: nrpLinkStatus } = useQuery({
    queryKey: ['nrp-link-status'],
    queryFn: api.nrp.getLinkStatus,
    enabled: true,
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

  const { data: groups } = useQuery({
    queryKey: ['groups'],
    queryFn: () => api.groups.list(),
    enabled: tab === 'settings',
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
  const isOwner = session?.user?.id === project.owner_id;
  const isSiteAdmin = session?.roles?.includes('admin') ?? false;
  const canManageProviders = isSiteAdmin || (isOwner && (session?.roles?.includes('project_creator') ?? false));
  const canDelete = isSiteAdmin || isOwner;


  const tabs: { key: Tab; label: string }[] = [
    { key: 'packages', label: 'Packages' },
    { key: 'analyses', label: 'Analyses' },
    { key: 'findings', label: 'Findings' },
    ...(isAdmin ? [{ key: 'api-keys' as Tab, label: 'LLMs' }] : []),
    ...(canEdit ? [{ key: 'github' as Tab, label: 'GitHub' }] : []),
    ...(nrpLinkStatus?.oauth_configured ? [{ key: 'nrp' as Tab, label: 'NRP' }] : []),
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
                  ? 'border-brand-600 text-brand-600'
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
        <FindingsTab projectId={id} projectName={project.name} packages={packages} canEdit={canEdit} />
      )}

      {/* API Keys tab */}
      {tab === 'api-keys' && isAdmin && (
        <ProviderKeysTab projectId={id} />
      )}

      {/* GitHub tab */}
      {tab === 'github' && canEdit && (
        <ProjectGitHubTab projectId={id} canManageInstallations={isAdmin} />
      )}

      {/* NRP tab */}
      {tab === 'nrp' && nrpLinkStatus?.oauth_configured && (
        <ProjectNRPTab projectId={id} isSystemAdmin={isSiteAdmin} isProjectAdmin={isAdmin || isSiteAdmin} />
      )}

      {/* Settings tab */}
      {tab === 'settings' && canEdit && (
        <SettingsTab
          project={project}
          groups={groups}
          canManageProviders={canManageProviders}
          canDelete={canDelete}
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
  const [editingId, setEditingId] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState('');
  const [gitUrl, setGitUrl] = useState('');
  const [gitBranch, setGitBranch] = useState('main');
  const [analysisPrompt, setAnalysisPrompt] = useState('');
  const [sarifUploadEnabled, setSarifUploadEnabled] = useState(false);
  const [branchError, setBranchError] = useState<string | null>(null);
  const [installationWarning, setInstallationWarning] = useState<string | null>(null);
  const [installationSuccess, setInstallationSuccess] = useState<string | null>(null);
  const [accessInstallURL, setAccessInstallURL] = useState<string | null>(null);
  const [needsGitHubLink, setNeedsGitHubLink] = useState(false);
  const [linkingGitHub, setLinkingGitHub] = useState(false);
  const [linkToProjectInstallationID, setLinkToProjectInstallationID] = useState<number | null>(null);
  const [linkToProjectError, setLinkToProjectError] = useState<string | null>(null);

  // Parse owner/repo from the git URL for display.
  const parsedGitHub = useMemo(() => {
    const m = gitUrl.match(/github\.com\/([^/]+)\/([^/.]+)/);
    return m ? { owner: m[1], repo: m[2] } : null;
  }, [gitUrl]);

  const { data: projectInstallations } = useQuery({
    queryKey: ['project-github-installations', projectId],
    queryFn: async () => {
      try {
        const resp = await api.github.listProjectInstallations(projectId);
        return resp.installations ?? [];
      } catch {
        return [];
      }
    },
  });
  const linkedProjectInstallationIDs = useMemo(
    () =>
      new Set(
        (projectInstallations ?? [])
          .filter((inst) => !!inst.linked_to_project)
          .map((inst) => inst.installation_id),
      ),
    [projectInstallations],
  );

  const formatInstallationRef = useCallback(
    (
      installationID: number,
      fallbackAccount: string,
      access?: {
        installation_account_login?: string;
        matched_installations?: Array<{ installation_id: number; account_login: string }>;
      },
    ) => {
      const account =
        access?.installation_account_login ||
        access?.matched_installations?.find((inst) => inst.installation_id === installationID)?.account_login ||
        fallbackAccount;
      return `${account} (${installationID})`;
    },
    [],
  );

  const setConnectedRepoMessage = useCallback((owner: string, repo: string) => {
    setInstallationSuccess(
      `Connected to ${owner}/${repo} via GitHub App install — results will be uploaded to GitHub Code Scanning.`,
    );
  }, []);

  const linkProjectInstallationMutation = useMutation({
    mutationFn: (installationId: number) => api.github.addProjectInstallation(projectId, installationId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['project-github-installations', projectId] });
      setLinkToProjectError(null);
      setLinkToProjectInstallationID(null);
      if (parsedGitHub && sarifUploadEnabled) {
        setInstallationWarning(null);
        setConnectedRepoMessage(parsedGitHub.owner, parsedGitHub.repo);
      }
    },
    onError: (error: unknown) => {
      setLinkToProjectError(error instanceof Error ? error.message : 'Failed to link GitHub App install to project.');
    },
  });

  const handleBranchDetection = useCallback((result: { ok: boolean; error?: string }) => {
    if (result.ok) {
      setBranchError(null);
      setNeedsGitHubLink(false);
      setLinkToProjectInstallationID(null);
      setLinkToProjectError(null);
      return;
    }
    // When branch detection fails, check repo access to give better advice.
    if (parsedGitHub) {
      api.github.userRepoAccess(parsedGitHub.owner, parsedGitHub.repo).then((access) => {
        if (access.accessible) {
          const installationID = access.installation_id ?? 0;
          if (installationID > 0 && !linkedProjectInstallationIDs.has(installationID)) {
            setLinkToProjectInstallationID(installationID);
            setLinkToProjectError(null);
            setInstallationWarning(
              `GitHub App install ${formatInstallationRef(installationID, parsedGitHub.owner, access)} can access ${parsedGitHub.owner}/${parsedGitHub.repo}. Click "Link to project" to enable package access in this project.`,
            );
            setInstallationSuccess(null);
          } else {
            setLinkToProjectInstallationID(null);
          }
          setBranchError(result.error ?? 'Could not detect branches.');
        } else if (access.needs_link && !access.has_installation) {
          setBranchError('Could not access this repository — link your GitHub account to check access.');
          setNeedsGitHubLink(true);
          setAccessInstallURL(null);
          setLinkToProjectInstallationID(null);
        } else if (!access.has_installation) {
          setBranchError('Could not access this repository — it may be private.');
          setAccessInstallURL(access.install_url ?? null);
          setNeedsGitHubLink(false);
          setLinkToProjectInstallationID(null);
        } else {
          setBranchError(access.error ?? 'GitHub App does not have access to this repository.');
          setAccessInstallURL(null);
          setNeedsGitHubLink(false);
          setLinkToProjectInstallationID(null);
        }
      }).catch(() => {
        setLinkToProjectInstallationID(null);
        setBranchError(result.error ?? 'Could not detect branches.');
      });
    } else {
      setLinkToProjectInstallationID(null);
      setBranchError(result.error ?? 'Could not detect branches.');
    }
  }, [formatInstallationRef, linkedProjectInstallationIDs, parsedGitHub]);

  const handleSarifToggle = useCallback(async (checked: boolean) => {
    setSarifUploadEnabled(checked);
    setInstallationWarning(null);
    setInstallationSuccess(null);
    setAccessInstallURL(null);
    setLinkToProjectInstallationID(null);
    setLinkToProjectError(null);
    if (!checked || !parsedGitHub) return;
    try {
      const access = await api.github.userRepoAccess(parsedGitHub.owner, parsedGitHub.repo);
      if (access.accessible) {
        const installationID = access.installation_id ?? 0;
        if (installationID > 0 && !linkedProjectInstallationIDs.has(installationID)) {
          setLinkToProjectInstallationID(installationID);
          setInstallationWarning(
            `GitHub App install ${formatInstallationRef(installationID, parsedGitHub.owner, access)} can access ${parsedGitHub.owner}/${parsedGitHub.repo}. Click "Link to project" to enable package access in this project.`,
          );
        } else {
          setConnectedRepoMessage(parsedGitHub.owner, parsedGitHub.repo);
        }
      } else if (!access.has_installation) {
        setInstallationWarning(
          `No GitHub App installation found for "${parsedGitHub.owner}". Results won't be uploaded until the app is installed.`
        );
        setAccessInstallURL(access.install_url ?? null);
      } else if (access.needs_link && !access.has_installation) {
        setInstallationWarning('Link your GitHub account to enable SARIF upload.');
        setNeedsGitHubLink(true);
      } else if (!access.accessible) {
        setInstallationWarning(
          access.error ?? `GitHub App does not have access to this repository.`
        );
      }
    } catch {
      // If the probe fails, don't block — the user can still enable it.
    }
  }, [formatInstallationRef, linkedProjectInstallationIDs, parsedGitHub, setConnectedRepoMessage]);

  // Re-check repo access when the user returns from the GitHub App install page.
  const branchInputRef = useRef<GitBranchInputHandle>(null);
  useEffect(() => {
    if (!parsedGitHub || (!branchError && !installationWarning)) return;

    const recheckAccess = () => {
      api.github.userRepoAccess(parsedGitHub.owner, parsedGitHub.repo).then((access) => {
        if (access.accessible) {
          setBranchError(null);
          const installationID = access.installation_id ?? 0;
          if (installationID > 0 && !linkedProjectInstallationIDs.has(installationID)) {
            setInstallationWarning(
              `GitHub App install ${formatInstallationRef(installationID, parsedGitHub.owner, access)} can access ${parsedGitHub.owner}/${parsedGitHub.repo}. Click "Link to project" to enable package access in this project.`,
            );
            setInstallationSuccess(null);
            setLinkToProjectInstallationID(installationID);
          } else {
            setInstallationWarning(null);
            setLinkToProjectInstallationID(null);
          }
          setAccessInstallURL(null);
          setNeedsGitHubLink(false);
          if (sarifUploadEnabled) {
            setConnectedRepoMessage(parsedGitHub.owner, parsedGitHub.repo);
          }
          branchInputRef.current?.refetch();
        } else if (!access.needs_link) {
          setNeedsGitHubLink(false);
          setLinkToProjectInstallationID(null);
        }
      }).catch(() => { /* ignore */ });
    };

    const onVisible = () => {
      if (document.visibilityState === 'visible') recheckAccess();
    };
    const onMessage = (e: MessageEvent) => {
      if (e.origin === window.location.origin && e.data?.type === 'github-linked') {
        setLinkingGitHub(false);
        setNeedsGitHubLink(false);
        recheckAccess();
      }
    };
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('message', onMessage);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('message', onMessage);
    };
  }, [formatInstallationRef, linkedProjectInstallationIDs, parsedGitHub, branchError, installationWarning, sarifUploadEnabled, setConnectedRepoMessage]);

  const createMutation = useMutation({
    mutationFn: () =>
      api.packages.create(projectId, {
        name,
        git_url: gitUrl,
        git_branch: gitBranch,
        analysis_prompt: analysisPrompt,
        sarif_upload_enabled: sarifUploadEnabled,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['packages', projectId] });
      setAdding(false);
      setName('');
      setGitUrl('');
      setGitBranch('main');
      setAnalysisPrompt('');
      setSarifUploadEnabled(false);
      setBranchError(null);
      setInstallationWarning(null);
      setInstallationSuccess(null);
      setLinkToProjectInstallationID(null);
      setLinkToProjectError(null);
    },
  });

  const updateMutation = useMutation({
    mutationFn: (pkgId: string) =>
      api.packages.update(projectId, pkgId, {
        name,
        git_url: gitUrl,
        git_branch: gitBranch,
        analysis_prompt: analysisPrompt,
        sarif_upload_enabled: sarifUploadEnabled,
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
    setSarifUploadEnabled(pkg.sarif_upload_enabled ?? false);
    setBranchError(null);
    setInstallationWarning(null);
    setInstallationSuccess(null);
    setLinkToProjectInstallationID(null);
    setLinkToProjectError(null);
    setAdding(false);
    // Auto-check access when editing a package with SARIF already enabled
    if (pkg.sarif_upload_enabled) {
      const m = pkg.git_url.match(/github\.com\/([^/]+)\/([^/.]+)/);
      if (m) {
        api.github.userRepoAccess(m[1], m[2]).then((access) => {
          if (access.accessible && access.has_installation) {
            const installationID = access.installation_id ?? 0;
            if (installationID > 0 && !linkedProjectInstallationIDs.has(installationID)) {
              setLinkToProjectInstallationID(installationID);
              setInstallationWarning(
                `GitHub App install ${formatInstallationRef(installationID, m[1], access)} can access ${m[1]}/${m[2]}. Click "Link to project" to enable package access in this project.`,
              );
            } else {
              setConnectedRepoMessage(m[1], m[2]);
            }
          }
        }).catch(() => { /* ignore */ });
      }
    }
  };

  const cancelEdit = () => {
    setEditingId(null);
    setName('');
    setGitUrl('');
    setGitBranch('main');
    setAnalysisPrompt('');
    setSarifUploadEnabled(false);
    setBranchError(null);
    setInstallationWarning(null);
    setInstallationSuccess(null);
    setLinkToProjectInstallationID(null);
    setLinkToProjectError(null);
  };

  const handleGitHubLink = async () => {
    setLinkingGitHub(true);
    try {
      const resp = await api.github.startLink();
      window.open(resp.authorize_url, 'github-link', 'width=600,height=700');
    } catch {
      setLinkingGitHub(false);
    }
  };

  return (
    <div>
      <div className="flex justify-between items-center mb-4">
        <h2 className="text-lg font-semibold">Software Packages</h2>
        {canEdit && (
          <button
            onClick={() => { setAdding(true); setEditingId(null); setName(''); setGitUrl(''); setGitBranch('main'); setAnalysisPrompt(''); setSarifUploadEnabled(false); setBranchError(null); setInstallationWarning(null); }}
            className="bg-brand-600 text-white px-3 py-1.5 text-sm rounded hover:bg-brand-700"
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
            {parsedGitHub && !branchError && (
              <p className="text-xs text-green-600 mt-1">
                Detected GitHub repo: {parsedGitHub.owner}/{parsedGitHub.repo}
              </p>
            )}
            {parsedGitHub && branchError && (
              <p className="text-xs text-amber-600 mt-1">
                {branchError}
                {needsGitHubLink && (
                  <>{' '}
                    <button
                      type="button"
                      onClick={handleGitHubLink}
                      disabled={linkingGitHub}
                      className="underline text-brand-600 hover:text-brand-700"
                    >
                      {linkingGitHub ? 'Linking…' : 'Link your GitHub account'}
                    </button>
                  </>
                )}
                {!needsGitHubLink && accessInstallURL && (
                  <>{' '}
                    <a href={accessInstallURL} target="_blank" rel="noopener noreferrer" className="underline text-brand-600 hover:text-brand-700">
                      Install the GitHub App
                    </a>{' '}
                    on <strong>{parsedGitHub.owner}</strong> to grant access.
                  </>
                )}
                    {linkToProjectInstallationID && (
                      <>{' '}
                        <button
                          type="button"
                          onClick={() => {
                            setLinkToProjectError(null);
                            linkProjectInstallationMutation.mutate(linkToProjectInstallationID);
                          }}
                          disabled={linkProjectInstallationMutation.isPending}
                          className="underline text-brand-600 hover:text-brand-700"
                        >
                          {linkProjectInstallationMutation.isPending ? 'Linking to project...' : 'Link to project'}
                        </button>
                      </>
                    )}
              </p>
            )}
                {linkToProjectError && (
                  <p className="text-xs text-red-600 mt-1">{linkToProjectError}</p>
                )}
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
              ref={branchInputRef}
              gitUrl={gitUrl}
              value={gitBranch}
              onChange={setGitBranch}
              projectId={projectId}
              onDetectionResult={handleBranchDetection}
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
          <div>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={sarifUploadEnabled}
                onChange={(e) => handleSarifToggle(e.target.checked)}
                className="rounded"
              />
              <span>Upload results to GitHub Code Scanning</span>
            </label>
            {installationWarning && (
              <p className="text-xs text-amber-600 mt-1">
                {installationWarning}
                {needsGitHubLink && (
                  <>{' '}
                    <button
                      type="button"
                      onClick={handleGitHubLink}
                      disabled={linkingGitHub}
                      className="underline text-brand-600 hover:text-brand-700"
                    >
                      {linkingGitHub ? 'Linking…' : 'Link your GitHub account'}
                    </button>
                  </>
                )}
                {!needsGitHubLink && accessInstallURL && (
                  <>{' '}
                    <a href={accessInstallURL} target="_blank" rel="noopener noreferrer" className="underline text-brand-600 hover:text-brand-700">
                      Install the GitHub App
                    </a>{' '}
                    to enable uploads.
                  </>
                )}
                {linkToProjectInstallationID && (
                  <>{' '}
                    <button
                      type="button"
                      onClick={() => {
                        setLinkToProjectError(null);
                        linkProjectInstallationMutation.mutate(linkToProjectInstallationID);
                      }}
                      disabled={linkProjectInstallationMutation.isPending}
                      className="underline text-brand-600 hover:text-brand-700"
                    >
                      {linkProjectInstallationMutation.isPending ? 'Linking to project...' : 'Link to project'}
                    </button>
                  </>
                )}
              </p>
            )}
            {linkToProjectError && (
              <p className="text-xs text-red-600 mt-1">{linkToProjectError}</p>
            )}
            {!installationWarning && installationSuccess && (
              <p className="text-xs text-green-600 mt-1 flex items-center gap-1">
                <svg className="h-3.5 w-3.5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20"><path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clipRule="evenodd" /></svg>
                {installationSuccess}
              </p>
            )}
          </div>
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={createMutation.isPending}
              className="bg-brand-600 text-white px-3 py-1.5 text-sm rounded hover:bg-brand-700 disabled:opacity-50"
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
                  {parsedGitHub && !branchError && (
                    <p className="text-xs text-green-600 mt-1">
                      Detected GitHub repo: {parsedGitHub.owner}/{parsedGitHub.repo}
                    </p>
                  )}
                  {parsedGitHub && branchError && (
                    <p className="text-xs text-amber-600 mt-1">
                      {branchError}
                      {needsGitHubLink && (
                        <>{' '}
                          <button
                            type="button"
                            onClick={handleGitHubLink}
                            disabled={linkingGitHub}
                            className="underline text-brand-600 hover:text-brand-700"
                          >
                            {linkingGitHub ? 'Linking…' : 'Link your GitHub account'}
                          </button>
                        </>
                      )}
                      {!needsGitHubLink && accessInstallURL && (
                        <>{' '}
                          <a href={accessInstallURL} target="_blank" rel="noopener noreferrer" className="underline text-brand-600 hover:text-brand-700">
                            Install the GitHub App
                          </a>{' '}
                          on <strong>{parsedGitHub.owner}</strong> to grant access.
                        </>
                      )}
                      {linkToProjectInstallationID && (
                        <>{' '}
                          <button
                            type="button"
                            onClick={() => {
                              setLinkToProjectError(null);
                              linkProjectInstallationMutation.mutate(linkToProjectInstallationID);
                            }}
                            disabled={linkProjectInstallationMutation.isPending}
                            className="underline text-brand-600 hover:text-brand-700"
                          >
                            {linkProjectInstallationMutation.isPending ? 'Linking to project...' : 'Link to project'}
                          </button>
                        </>
                      )}
                    </p>
                  )}
                  {linkToProjectError && (
                    <p className="text-xs text-red-600 mt-1">{linkToProjectError}</p>
                  )}
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
                    ref={branchInputRef}
                    gitUrl={gitUrl}
                    value={gitBranch}
                    onChange={setGitBranch}
                    projectId={projectId}
                    packageId={pkg.id}
                    onDetectionResult={handleBranchDetection}
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
                <div>
                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={sarifUploadEnabled}
                      onChange={(e) => handleSarifToggle(e.target.checked)}
                      className="rounded"
                    />
                    <span>Upload results to GitHub Code Scanning</span>
                  </label>
                  {installationWarning && (
                    <p className="text-xs text-amber-600 mt-1">
                      {installationWarning}
                      {needsGitHubLink && (
                        <>{' '}
                          <button
                            type="button"
                            onClick={handleGitHubLink}
                            disabled={linkingGitHub}
                            className="underline text-brand-600 hover:text-brand-700"
                          >
                            {linkingGitHub ? 'Linking…' : 'Link your GitHub account'}
                          </button>
                        </>
                      )}
                      {!needsGitHubLink && accessInstallURL && (
                        <>{' '}
                          <a href={accessInstallURL} target="_blank" rel="noopener noreferrer" className="underline text-brand-600 hover:text-brand-700">
                            Install the GitHub App
                          </a>{' '}
                          to enable uploads.
                        </>
                      )}
                      {linkToProjectInstallationID && (
                        <>{' '}
                          <button
                            type="button"
                            onClick={() => {
                              setLinkToProjectError(null);
                              linkProjectInstallationMutation.mutate(linkToProjectInstallationID);
                            }}
                            disabled={linkProjectInstallationMutation.isPending}
                            className="underline text-brand-600 hover:text-brand-700"
                          >
                            {linkProjectInstallationMutation.isPending ? 'Linking to project...' : 'Link to project'}
                          </button>
                        </>
                      )}
                    </p>
                  )}
                  {linkToProjectError && (
                    <p className="text-xs text-red-600 mt-1">{linkToProjectError}</p>
                  )}
                  {!installationWarning && installationSuccess && (
                    <p className="text-xs text-green-600 mt-1 flex items-center gap-1">
                      <svg className="h-3.5 w-3.5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20"><path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clipRule="evenodd" /></svg>
                      {installationSuccess}
                    </p>
                  )}
                </div>
                <div className="flex gap-2">
                  <button
                    type="submit"
                    disabled={updateMutation.isPending}
                    className="bg-brand-600 text-white px-3 py-1.5 text-sm rounded hover:bg-brand-700 disabled:opacity-50"
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
                  {pkg.github_owner && pkg.github_repo && (
                    <p className="text-xs text-gray-400">
                      GitHub: {pkg.github_owner}/{pkg.github_repo}
                      {pkg.sarif_upload_enabled && (
                        <span className="text-green-600"> · Code Scanning enabled</span>
                      )}
                    </p>
                  )}
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
                        className="text-brand-500 text-sm hover:text-brand-700"
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
  const [agentModel, setAgentModel] = useState('');
  const [selectedProvider, setSelectedProvider] = useState('');
  const [analysisPage, setAnalysisPage] = useState(1);

  // Fetch available providers (global + project)
  const { data: availableProviders } = useQuery({
    queryKey: ['available-providers', projectId],
    queryFn: () => api.availableProviders(projectId),
    staleTime: 60_000,
  });

  // Auto-select the first available provider when none is explicitly chosen.
  const effectiveProvider = selectedProvider
    || (availableProviders?.length ? `${availableProviders[0].source}:${availableProviders[0].id}` : '');

  // Legacy agent status (fallback when no providers are configured)
  const { data: agentStatus } = useQuery({
    queryKey: ['agent-status'],
    queryFn: () => api.agent.status(),
    staleTime: 60_000,
  });

  const hasProviders = availableProviders && availableProviders.length > 0;

  // Parse selected provider
  const selectedProviderObj = availableProviders?.find(
    (p) => `${p.source}:${p.id}` === effectiveProvider
  );

  // Discover models for selected provider
  const { data: discoveredModels, isFetching: loadingModels } = useQuery({
    queryKey: ['discovered-models', effectiveProvider],
    queryFn: () => {
      if (!selectedProviderObj) return Promise.resolve([]);
      if (selectedProviderObj.source === 'global' || selectedProviderObj.source === 'env') {
        return api.discoverAvailableProviderModels(projectId, selectedProviderObj.source, selectedProviderObj.id);
      }
      return api.providerKeys.discoverModels(projectId, selectedProviderObj.id);
    },
    enabled: !!selectedProviderObj,
    staleTime: 5 * 60_000,
  });

  // Determine if the agent is ready (either via providers or legacy config)
  const agentReady = hasProviders || agentStatus?.ready;

  const triggerMutation = useMutation({
    mutationFn: () => {
      // Resolve concrete model: user selection → provider default → first discovered model.
      const effectiveModel = agentModel || selectedProviderObj?.default_model || discoveredModels?.[0]?.id || undefined;
      const data: { package_ids: string[]; agent_model?: string; custom_prompt?: string; provider_id?: string; provider_source?: string } = {
        package_ids: selectedPkgs,
        agent_model: effectiveModel,
        custom_prompt: customPrompt || undefined,
      };
      if (selectedProviderObj) {
        data.provider_id = selectedProviderObj.id;
        data.provider_source = selectedProviderObj.source;
      }
      return api.analyses.create(projectId, data);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['analyses', projectId] });
      setSelectedPkgs([]);
      setCustomPrompt('');
      setAgentModel('');
    },
  });

  return (
    <div>
      {/* Trigger new analysis */}
      {canEdit && packages && packages.length > 0 && (
        <div className="bg-gray-50 p-4 rounded border mb-6">
          <h3 className="text-sm font-medium mb-2">Run New Analysis</h3>
          {!agentReady && (
            <div className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded p-2 mb-3">
              No LLM providers are configured. Ask an admin to add a provider in Settings, or set <code className="bg-amber-100 px-1 rounded">AGENT_API_KEY</code>.
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

          {/* Provider selection */}
          {hasProviders ? (
            <>
              <div className="mb-3">
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  Provider
                </label>
                <select
                  value={effectiveProvider}
                  onChange={(e) => {
                    setSelectedProvider(e.target.value);
                    setAgentModel('');
                  }}
                  className="w-full border rounded px-3 py-2 text-sm bg-white"
                >
                  {availableProviders.map((p) => (
                    <option key={`${p.source}:${p.id}`} value={`${p.source}:${p.id}`}>
                      {p.label} ({p.api_schema}){p.source === 'project' ? ' — project' : p.source === 'env' ? ' — env' : ''}
                    </option>
                  ))}
                </select>
              </div>
              <div className="mb-3">
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  Model
                </label>
                {loadingModels ? (
                  <p className="text-xs text-gray-500 py-2">Discovering models...</p>
                ) : discoveredModels && discoveredModels.length > 0 ? (
                  <select
                    value={agentModel}
                    onChange={(e) => setAgentModel(e.target.value)}
                    className="w-full border rounded px-3 py-2 text-sm bg-white"
                  >
                    <option value="">
                      {selectedProviderObj?.default_model
                        ? `Default (${selectedProviderObj.default_model})`
                        : 'Auto (provider default)'}
                    </option>
                    {discoveredModels.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.display_name || m.id}
                      </option>
                    ))}
                  </select>
                ) : (
                  <>
                    <input
                      type="text"
                      value={agentModel}
                      onChange={(e) => setAgentModel(e.target.value)}
                      placeholder={selectedProviderObj?.default_model || 'Auto (provider default)'}
                      className="w-full border rounded px-3 py-2 text-sm bg-white"
                    />
                    <p className="text-xs text-gray-500 mt-1">
                      {effectiveProvider ? 'Could not discover models. Enter a model ID manually or leave blank.' : 'Select a provider to discover available models.'}
                    </p>
                  </>
                )}
              </div>
            </>
          ) : (
            /* No providers available for this project */
            <div className="mb-3 text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded p-3">
              No LLM providers are available for this project. An admin must allow providers for this project in the <strong>Settings → Provider Access</strong> tab.
            </div>
          )}

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
            disabled={!selectedPkgs.length || triggerMutation.isPending || !agentReady || (hasProviders && !selectedProviderObj)}
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
                {a.error_message && (
                  <p className="text-sm text-red-600 mt-1 font-mono break-words">
                    {a.error_message}
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
  projectName,
  packages,
  canEdit,
}: {
  projectId: string;
  projectName: string;
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
        projectName={projectName}
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
  groups,
  canManageProviders,
  canDelete,
  onDelete,
}: {
  project: Project;
  groups?: Group[];
  canManageProviders?: boolean;
  canDelete?: boolean;
  onDelete: () => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description);
  const [readGroupId, setReadGroupId] = useState(project.read_group_id ?? '');
  const [writeGroupId, setWriteGroupId] = useState(project.write_group_id ?? '');
  const [adminGroupId, setAdminGroupId] = useState(project.admin_group_id ?? '');

  const updateMutation = useMutation({
    mutationFn: () =>
      api.projects.update(project.id, {
        name,
        description,
        read_group_id: readGroupId || null,
        write_group_id: writeGroupId || null,
        admin_group_id: adminGroupId || null,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['project', project.id] });
      queryClient.invalidateQueries({ queryKey: ['projects'] });
    },
  });

  // All enabled global/env providers (unfiltered)
  const { data: allProviders } = useQuery({
    queryKey: ['all-providers', project.id],
    queryFn: () => api.allProviders(project.id),
    enabled: !!canManageProviders,
  });

  // Currently allowed providers for this project
  const { data: allowedProviders } = useQuery({
    queryKey: ['allowed-providers', project.id],
    queryFn: () => api.allowedProviders.list(project.id),
    enabled: !!canManageProviders,
  });

  const allowedSet = new Set(
    (allowedProviders ?? []).map((a) => `${a.provider_source}:${a.provider_id}`)
  );

  const toggleProviderMut = useMutation({
    mutationFn: ({ providerId, providerSource, allowed }: { providerId: string; providerSource: string; allowed: boolean }) => {
      if (allowed) {
        return api.allowedProviders.remove(project.id, providerId, providerSource);
      }
      return api.allowedProviders.add(project.id, providerId, providerSource);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['allowed-providers', project.id] });
      queryClient.invalidateQueries({ queryKey: ['available-providers', project.id] });
    },
  });

  // Filter to only env and global providers (not project keys)
  const systemProviders = (allProviders ?? []).filter((p) => p.source === 'env' || p.source === 'global');

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

        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <GroupSelect
            label="Read Access Group"
            value={readGroupId}
            groups={groups}
            onChange={setReadGroupId}
          />
          <GroupSelect
            label="Write Access Group"
            value={writeGroupId}
            groups={groups}
            onChange={setWriteGroupId}
          />
          <GroupSelect
            label="Admin Group"
            value={adminGroupId}
            groups={groups}
            onChange={setAdminGroupId}
          />
        </div>

        <button
          type="submit"
          disabled={updateMutation.isPending}
          className="bg-brand-600 text-white px-4 py-2 rounded hover:bg-brand-700 disabled:opacity-50"
        >
          {updateMutation.isPending ? 'Saving...' : 'Save Changes'}
        </button>
        {updateMutation.isSuccess && (
          <span className="text-green-600 text-sm ml-3">Saved!</span>
        )}
      </form>

      {/* Provider Access — only visible to admins/project_creators */}
      {canManageProviders && (
        <div className="border-t pt-6">
          <h3 className="text-lg font-semibold mb-2">Provider Access</h3>
          <p className="text-sm text-gray-500 mb-3">
            Control which global and environment providers this project can use for analyses.
            Project-owned API keys are always available.
          </p>
          {systemProviders.length > 0 ? (
            <div className="border rounded-md divide-y">
              {systemProviders.map((p) => {
                const key = `${p.source}:${p.id}`;
                const isAllowed = allowedSet.has(key);
                return (
                  <div key={key} className="p-3 flex items-center justify-between">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="font-medium text-sm">{p.label}</span>
                        <span className={`px-1.5 py-0.5 text-xs rounded ${
                          p.api_schema === 'anthropic' ? 'bg-purple-100 text-purple-700' : 'bg-green-100 text-green-700'
                        }`}>
                          {p.api_schema}
                        </span>
                        <span className={`px-1.5 py-0.5 text-xs rounded ${
                          p.source === 'env' ? 'bg-blue-100 text-blue-700' : 'bg-gray-100 text-gray-600'
                        }`}>
                          {p.source}
                        </span>
                      </div>
                      {p.default_model && (
                        <div className="text-xs text-gray-500 mt-0.5">Default model: {p.default_model}</div>
                      )}
                    </div>
                    <button
                      onClick={() => toggleProviderMut.mutate({
                        providerId: p.id,
                        providerSource: p.source,
                        allowed: isAllowed,
                      })}
                      disabled={toggleProviderMut.isPending}
                      className={`px-2 py-1 text-xs rounded ${
                        isAllowed
                          ? 'bg-green-100 text-green-700 hover:bg-green-200'
                          : 'bg-gray-100 text-gray-500 hover:bg-gray-200'
                      }`}
                    >
                      {isAllowed ? 'Allowed' : 'Not Allowed'}
                    </button>
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="text-sm text-gray-400 text-center py-4 border rounded-md">
              No global or environment providers configured. Add providers in{' '}
              <a href="/admin/settings" className="text-brand-600 hover:underline">Admin Settings</a>{' '}
              to control access here.
            </div>
          )}
        </div>
      )}

      {canDelete && (
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
      )}
    </div>
  );
}

function GroupSelect({
  label,
  value,
  groups,
  onChange,
}: {
  label: string;
  value: string;
  groups?: Group[];
  onChange: (value: string) => void;
}) {
  return (
    <div>
      <label className="block text-sm font-medium text-gray-700 mb-1">
        {label}
      </label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full border rounded px-3 py-2"
      >
        <option value="">None</option>
        {groups?.map((g) => (
          <option key={g.id} value={g.id}>
            {g.name}
          </option>
        ))}
      </select>
    </div>
  );
}

function ProviderKeysTab({ projectId }: { projectId: string }) {
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [provider, setProvider] = useState('anthropic');
  const [label, setLabel] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [endpointUrl, setEndpointUrl] = useState('');

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
        ...(provider === 'custom' || (provider === 'nrp' && endpointUrl)
          ? { endpoint_url: endpointUrl }
          : {}),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-keys', projectId] });
      setAdding(false);
      setLabel('');
      setApiKey('');
      setEndpointUrl('');
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
          <h2 className="text-lg font-semibold">LLM Providers</h2>
          <p className="text-sm text-gray-500">
            API keys are encrypted at rest. Only the last 4 characters are ever displayed.
          </p>
        </div>
        {!adding && (
          <button
            onClick={() => setAdding(true)}
            className="bg-brand-600 text-white px-4 py-2 rounded hover:bg-brand-700 text-sm"
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
              <option value="nrp">NRP (ACCESS)</option>
              <option value="custom">Custom Endpoint</option>
            </select>
          </div>
          {(provider === 'custom' || provider === 'nrp') && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Endpoint URL {provider === 'custom' && <span className="text-red-500">*</span>}
              </label>
              <input
                type="url"
                value={endpointUrl}
                onChange={(e) => setEndpointUrl(e.target.value)}
                required={provider === 'custom'}
                placeholder="https://api.example.com/v1"
                className="w-full border rounded px-3 py-2 font-mono text-sm"
              />
              {provider === 'nrp' && (
                <p className="text-xs text-gray-500 mt-1">
                  Optional. Leave empty to use the global NRP endpoint.
                </p>
              )}
            </div>
          )}
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
              className="bg-brand-600 text-white px-4 py-2 rounded hover:bg-brand-700 disabled:opacity-50 text-sm"
            >
              {createMutation.isPending ? 'Saving...' : 'Save Key'}
            </button>
            <button
              type="button"
              onClick={() => {
                setAdding(false);
                setApiKey('');
                setEndpointUrl('');
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
                  {k.endpoint_url && (
                    <span className="ml-2" title={k.endpoint_url}>
                      Endpoint: <code>{k.endpoint_url}</code>
                    </span>
                  )}
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
