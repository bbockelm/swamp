'use client';

import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, type GitHubWebhookDelivery } from '@/lib/api';

export function ProjectGitHubTab({
  projectId,
  canManageInstallations,
}: {
  projectId: string;
  canManageInstallations: boolean;
}) {
  const [linkingGitHub, setLinkingGitHub] = useState(false);
  const [selectedInstallationId, setSelectedInstallationId] = useState(0);
  const queryClient = useQueryClient();

  const { data: linkStatus } = useQuery({
    queryKey: ['github-link-status'],
    queryFn: () => api.github.getLinkStatus(),
    staleTime: 30_000,
  });

  const { data: installations, isLoading } = useQuery({
    queryKey: ['project-github-installations', projectId],
    queryFn: async () => {
      const resp = await api.github.listProjectInstallations(projectId);
      return resp.installations ?? [];
    },
  });

  const { data: allInstallations } = useQuery({
    queryKey: ['github-installations'],
    queryFn: async () => {
      try {
        const resp = await api.github.listInstallations();
        return resp.installations ?? [];
      } catch {
        return [];
      }
    },
    enabled: canManageInstallations,
  });

  const { data: appInfo } = useQuery({
    queryKey: ['github-app-info'],
    queryFn: () => api.github.appInfo(),
    staleTime: 300_000,
  });

  const addLinkMutation = useMutation({
    mutationFn: (installationId: number) => api.github.addProjectInstallation(projectId, installationId),
    onSuccess: () => {
      setSelectedInstallationId(0);
      queryClient.invalidateQueries({ queryKey: ['github-installations'] });
      queryClient.invalidateQueries({ queryKey: ['project-github-installations', projectId] });
    },
  });

  const removeLinkMutation = useMutation({
    mutationFn: (installationId: number) => api.github.removeProjectInstallation(projectId, installationId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['github-installations'] });
      queryClient.invalidateQueries({ queryKey: ['project-github-installations', projectId] });
    },
  });

  const linkedInstallationIDs = useMemo(() => {
    const ids = new Set<number>();
    for (const inst of installations ?? []) {
      if (inst.linked_to_project) {
        ids.add(inst.installation_id);
      }
    }
    return ids;
  }, [installations]);

  const linkableInstallations = useMemo(
    () => (allInstallations ?? []).filter((inst) => !linkedInstallationIDs.has(inst.installation_id)),
    [allInstallations, linkedInstallationIDs],
  );

  const displayInstallations = useMemo(() => {
    const merged = [...(installations ?? [])];
    if (canManageInstallations) {
      const seen = new Set(merged.map((inst) => inst.installation_id));
      for (const inst of allInstallations ?? []) {
        if (seen.has(inst.installation_id)) {
          continue;
        }
        merged.push({
          ...inst,
          linked_to_project: false,
          packages: [],
        });
      }
    }

    merged.sort((a, b) => {
      if (a.linked_to_project !== b.linked_to_project) {
        return a.linked_to_project ? -1 : 1;
      }
      return a.account_login.localeCompare(b.account_login);
    });

    return merged;
  }, [allInstallations, canManageInstallations, installations]);

  useEffect(() => {
    const handler = (e: MessageEvent) => {
      if (e.origin === window.location.origin && e.data?.type === 'github-linked') {
        setLinkingGitHub(false);
        queryClient.invalidateQueries({ queryKey: ['github-link-status'] });
        queryClient.invalidateQueries({ queryKey: ['github-installations'] });
        queryClient.invalidateQueries({ queryKey: ['project-github-installations', projectId] });
      }
    };
    window.addEventListener('message', handler);
    return () => window.removeEventListener('message', handler);
  }, [projectId, queryClient]);

  const handleLinkGitHub = async () => {
    setLinkingGitHub(true);
    try {
      const resp = await api.github.startLink();
      window.open(resp.authorize_url, 'github-link', 'width=600,height=700');
    } catch {
      setLinkingGitHub(false);
    }
  };

  if (isLoading) return <p className="text-sm text-gray-500">Loading…</p>;

  return (
    <div className="space-y-6">
      {linkStatus && !linkStatus.linked && linkStatus.oauth_configured && (
        <div className="bg-amber-50 border border-amber-200 p-4 rounded-lg flex items-center justify-between">
          <div>
            <p className="text-sm font-medium text-amber-800">GitHub account not linked</p>
            <p className="text-xs text-amber-600 mt-0.5">
              Link your GitHub account to claim installations and enable repository access checks.
            </p>
          </div>
          <button
            onClick={handleLinkGitHub}
            disabled={linkingGitHub}
            className="bg-brand-600 text-white px-3 py-1.5 text-sm rounded hover:bg-brand-700 disabled:opacity-50 whitespace-nowrap"
          >
            {linkingGitHub ? 'Linking…' : 'Link GitHub Account'}
          </button>
        </div>
      )}

      {linkStatus?.linked && (
        <div className="text-xs text-gray-500 flex items-center gap-1.5">
          <svg className="w-3.5 h-3.5 text-green-500" fill="currentColor" viewBox="0 0 16 16">
            <path fillRule="evenodd" d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
          </svg>
          Linked as <span className="font-medium">{linkStatus.github_login}</span>
        </div>
      )}

      {appInfo?.configured && appInfo.install_url && (
        <div className="bg-white p-6 rounded-lg border">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-lg font-semibold">GitHub App</h2>
              <p className="text-sm text-gray-500 mt-1">
                Install the GitHub App on your organizations to enable private repo access, branch detection, and SARIF uploads.
              </p>
            </div>
            <a
              href={appInfo.install_url}
              target="_blank"
              rel="noopener noreferrer"
              className="bg-brand-600 text-white px-4 py-2 rounded hover:bg-brand-700 text-sm whitespace-nowrap"
            >
              Install GitHub App
            </a>
          </div>
        </div>
      )}

      <div className="bg-white p-6 rounded-lg border">
        <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
          <h2 className="text-lg font-semibold">GitHub App Installations</h2>
          {canManageInstallations && (
            <div className="flex items-center gap-2">
              <select
                value={selectedInstallationId}
                onChange={(e) => setSelectedInstallationId(Number(e.target.value))}
                className="border rounded px-2 py-1.5 text-sm"
              >
                <option value={0}>Link an installation to this project...</option>
                {linkableInstallations.map((inst) => (
                  <option key={inst.installation_id} value={inst.installation_id}>
                    {inst.account_login} ({inst.account_type}) #{inst.installation_id}
                  </option>
                ))}
              </select>
              <button
                type="button"
                disabled={selectedInstallationId <= 0 || addLinkMutation.isPending || removeLinkMutation.isPending}
                onClick={() => addLinkMutation.mutate(selectedInstallationId)}
                className="bg-brand-600 text-white px-3 py-1.5 text-sm rounded hover:bg-brand-700 disabled:opacity-50"
              >
                {addLinkMutation.isPending ? 'Linking...' : 'Link'}
              </button>
            </div>
          )}
        </div>

        <p className="text-xs text-gray-500 mb-3">
          Package onboarding checks repository access during package creation. Project installation links here are a separate, complementary workflow controlling which GitHub App installs are enabled for this project.
        </p>

        {!displayInstallations.length ? (
          <p className="text-sm text-gray-500">
            No project-visible GitHub App installations found. Link one to this project to enable repository operations.
          </p>
        ) : (
          <div className="border rounded divide-y">
            {displayInstallations.map((inst) => {
              const linkedToProject = !!inst.linked_to_project;
              const packageCount = inst.packages?.length ?? 0;
              const statusLabel = linkedToProject
                ? 'Linked to project'
                : packageCount > 0
                  ? 'Referenced by package(s)'
                  : 'Not linked';
              const statusClass = linkedToProject
                ? 'text-green-700 bg-green-50 border-green-200'
                : packageCount > 0
                  ? 'text-blue-700 bg-blue-50 border-blue-200'
                  : 'text-gray-600 bg-gray-50 border-gray-200';
              return (
                <div key={inst.installation_id} className="px-4 py-3 flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <div className="w-8 h-8 bg-gray-100 rounded-full flex items-center justify-center">
                      <svg className="w-4 h-4 text-gray-500" fill="currentColor" viewBox="0 0 16 16">
                        {inst.account_type === 'Organization' ? (
                          <path fillRule="evenodd" d="M1.5 14.25c0 .138.112.25.25.25H4v-1.25a.75.75 0 01.75-.75h2.5a.75.75 0 01.75.75v1.25h2.25a.25.25 0 00.25-.25V1.75a.25.25 0 00-.25-.25h-8.5a.25.25 0 00-.25.25v12.5zM1.75 16A1.75 1.75 0 010 14.25V1.75C0 .784.784 0 1.75 0h8.5C11.216 0 12 .784 12 1.75v12.5c0 .085-.006.168-.018.25h2.268a.25.25 0 00.25-.25V8.285a.25.25 0 00-.111-.208l-1.055-.703a.75.75 0 11.832-1.248l1.055.703c.487.325.779.871.779 1.456v5.965A1.75 1.75 0 0114.25 16h-3.5a.75.75 0 01-.197-.026c-.099.017-.2.026-.303.026h-3a.75.75 0 01-.75-.75V14h-1v1.25a.75.75 0 01-.75.75h-3zM3 3.75A.75.75 0 013.75 3h.5a.75.75 0 010 1.5h-.5A.75.75 0 013 3.75zM3.75 6a.75.75 0 000 1.5h.5a.75.75 0 000-1.5h-.5zM3 9.75A.75.75 0 013.75 9h.5a.75.75 0 010 1.5h-.5A.75.75 0 013 9.75zM7.75 9a.75.75 0 000 1.5h.5a.75.75 0 000-1.5h-.5zM7 6.75A.75.75 0 017.75 6h.5a.75.75 0 010 1.5h-.5A.75.75 0 017 6.75zM7.75 3a.75.75 0 000 1.5h.5a.75.75 0 000-1.5h-.5z" />
                        ) : (
                          <path fillRule="evenodd" d="M10.5 5a2.5 2.5 0 11-5 0 2.5 2.5 0 015 0zm.061 3.073a4 4 0 10-5.123 0 6.004 6.004 0 00-3.431 5.142.75.75 0 001.498.07 4.5 4.5 0 018.99 0 .75.75 0 101.498-.07 6.005 6.005 0 00-3.432-5.142z" />
                        )}
                      </svg>
                    </div>
                    <div>
                      <p className="font-medium text-sm">{inst.account_login}</p>
                      <p className="text-xs text-gray-400">
                        {inst.account_type} · Installation #{inst.installation_id}
                      </p>
                    </div>
                  </div>

                  <div className="flex flex-col items-end gap-1.5 shrink-0">
                    <span className={`text-xs border px-2 py-1 rounded ${statusClass}`}>
                      {statusLabel}
                    </span>

                    {inst.enabled_at && (
                      <span className="text-xs text-gray-400">
                        Enabled {new Date(inst.enabled_at).toLocaleString()}
                        {inst.enabled_by_name ? ` by ${inst.enabled_by_name}` : ''}
                      </span>
                    )}

                    {packageCount > 0 ? (
                      <div className="flex flex-wrap gap-1 justify-end max-w-md">
                        {inst.packages.map((p) => (
                          <span key={p.id} className="text-xs text-gray-600 bg-gray-100 px-1.5 py-0.5 rounded font-mono">
                            {p.github_owner && p.github_repo ? `${p.github_owner}/${p.github_repo}` : p.name}
                          </span>
                        ))}
                      </div>
                    ) : (
                      <span className="text-xs text-gray-400 italic">No packages in this project currently use this installation</span>
                    )}

                    {canManageInstallations && (
                      linkedToProject ? (
                        <button
                          type="button"
                          disabled={removeLinkMutation.isPending || addLinkMutation.isPending}
                          onClick={() => removeLinkMutation.mutate(inst.installation_id)}
                          className="text-xs text-red-700 bg-red-50 border border-red-200 px-2 py-1 rounded hover:bg-red-100 disabled:opacity-50"
                        >
                          {removeLinkMutation.isPending ? 'Unlinking...' : 'Unlink from project'}
                        </button>
                      ) : (
                        <button
                          type="button"
                          disabled={addLinkMutation.isPending || removeLinkMutation.isPending}
                          onClick={() => addLinkMutation.mutate(inst.installation_id)}
                          className="text-xs text-brand-700 bg-brand-50 border border-brand-200 px-2 py-1 rounded hover:bg-brand-100 disabled:opacity-50"
                        >
                          {addLinkMutation.isPending ? 'Linking...' : 'Link to project'}
                        </button>
                      )
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}

        <p className="text-xs text-gray-400 mt-3">
          Linked installations are available to the whole project. Referenced by package(s) means at least one package repository owner matches that installation account.
        </p>
      </div>

      <WebhookDeliveries projectId={projectId} />
    </div>
  );
}

function WebhookDeliveries({ projectId }: { projectId: string }) {
  const { data: webhooks, isLoading } = useQuery<GitHubWebhookDelivery[]>({
    queryKey: ['project', projectId, 'github-webhooks'],
    queryFn: () => api.github.listWebhooks(projectId),
  });

  return (
    <div className="bg-white p-6 rounded-lg border space-y-4">
      <h2 className="text-lg font-semibold">Recent Webhook Deliveries</h2>
      {isLoading ? (
        <p className="text-sm text-gray-500">Loading webhook deliveries…</p>
      ) : !webhooks?.length ? (
        <p className="text-sm text-gray-500">No webhook deliveries recorded yet for this project.</p>
      ) : (
        <div className="border rounded divide-y max-h-96 overflow-y-auto">
          {webhooks.map((d) => (
            <div key={d.id} className="px-3 py-2 text-sm flex items-center justify-between">
              <div>
                <span className="font-medium">{d.event_type}</span>
                {d.action && <span className="text-gray-500 ml-1">({d.action})</span>}
                <span className="text-gray-400 ml-2">{d.sender_login}</span>
                {d.ref && <span className="text-gray-400 ml-2">{d.ref}</span>}
              </div>
              <div className="flex items-center gap-2">
                <span
                  className={`text-xs px-2 py-0.5 rounded ${
                    d.status === 'processed'
                      ? 'bg-green-100 text-green-700'
                      : d.status === 'error'
                        ? 'bg-red-100 text-red-700'
                        : 'bg-gray-100 text-gray-600'
                  }`}
                >
                  {d.status}
                </span>
                <span className="text-xs text-gray-400">
                  {new Date(d.created_at).toLocaleString()}
                </span>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
