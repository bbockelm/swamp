'use client';

import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';

export function ProjectNRPTab({
  projectId,
  isSystemAdmin,
  isProjectAdmin,
}: {
  projectId: string;
  isSystemAdmin: boolean;
  isProjectAdmin: boolean;
}) {
  const queryClient = useQueryClient();
  const [linkingNRP, setLinkingNRP] = useState(false);
  const [linkMessage, setLinkMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);

  const { data: linkStatus } = useQuery({
    queryKey: ['nrp-link-status'],
    queryFn: () => api.nrp.getLinkStatus(),
    staleTime: 30_000,
  });

  const { data: config, isLoading } = useQuery({
    queryKey: ['project-nrp-config', projectId],
    queryFn: () => api.nrp.getProjectConfig(projectId),
  });

  const updateMutation = useMutation({
    mutationFn: (data: { access_enabled?: boolean; execution_enabled?: boolean }) =>
      api.nrp.updateProjectConfig(projectId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['project-nrp-config', projectId] });
    },
    onError: (error: Error) => {
      setLinkMessage({ type: 'error', text: error.message || 'Failed to update NRP settings.' });
    },
  });

  const canLinkedProjectAdminManageAccess = !!isProjectAdmin && !!linkStatus?.linked;
  const canManageProjectAccess = isSystemAdmin || canLinkedProjectAdminManageAccess;

  useEffect(() => {
    const handler = (e: MessageEvent) => {
      if (e.origin !== window.location.origin) {
        return;
      }
      if (e.data?.type !== 'identity-link-result' || e.data?.provider !== 'nrp') {
        return;
      }
      setLinkingNRP(false);
      if (e.data?.status === 'error') {
        setLinkMessage({ type: 'error', text: e.data?.message || 'Failed to link NRP account.' });
        return;
      }
      setLinkMessage({ type: 'success', text: e.data?.message || 'NRP account linked.' });
      queryClient.invalidateQueries({ queryKey: ['nrp-link-status'] });
      queryClient.invalidateQueries({ queryKey: ['project-nrp-config', projectId] });
    };
    window.addEventListener('message', handler);
    return () => window.removeEventListener('message', handler);
  }, [projectId, queryClient]);

  const handleLinkNRP = async () => {
    setLinkMessage(null);
    setLinkingNRP(true);
    try {
      const resp = await api.nrp.startLink();
      const popup = window.open(resp.authorize_url, 'nrp-link', 'width=600,height=700');
      if (!popup) {
        setLinkingNRP(false);
        setLinkMessage({ type: 'error', text: 'Popup blocked. Allow popups and try again.' });
      }
    } catch (error) {
      setLinkingNRP(false);
      setLinkMessage({ type: 'error', text: error instanceof Error ? error.message : 'Failed to start NRP linking.' });
    }
  };

  if (isLoading) {
    return <p className="text-sm text-gray-500">Loading…</p>;
  }

  return (
    <div className="space-y-6">
      {linkMessage && (
        <div className={`border rounded-lg px-4 py-3 text-sm ${linkMessage.type === 'success' ? 'bg-green-50 border-green-200 text-green-800' : 'bg-red-50 border-red-200 text-red-800'}`}>
          {linkMessage.text}
        </div>
      )}

      {linkStatus && !linkStatus.linked && linkStatus.oauth_configured && (
        <div className="bg-amber-50 border border-amber-200 p-4 rounded-lg flex items-center justify-between gap-4">
          <div>
            <p className="text-sm font-medium text-amber-800">NRP identity not linked</p>
            <p className="text-xs text-amber-600 mt-0.5">
              Link your NRP account to use NRP-backed project features. This uses the same account-linking flow as GitHub.
            </p>
          </div>
          <button
            onClick={handleLinkNRP}
            disabled={linkingNRP}
            className="bg-brand-600 text-white px-3 py-1.5 text-sm rounded hover:bg-brand-700 disabled:opacity-50 whitespace-nowrap"
          >
            {linkingNRP ? 'Linking…' : 'Link NRP Account'}
          </button>
        </div>
      )}

      {linkStatus?.linked && (
        <div className="text-xs text-gray-500 flex items-center gap-1.5">
          <svg className="w-3.5 h-3.5 text-green-500" fill="currentColor" viewBox="0 0 16 16">
            <path d="M6.173 14.727L.466 9.02l1.414-1.414 4.293 4.293L14.12-.049l1.414 1.414z" />
          </svg>
          Linked as <span className="font-medium">{linkStatus.nrp_login}</span>
        </div>
      )}

      <div className="bg-white p-6 rounded-lg border space-y-3">
        <div>
          <h2 className="text-lg font-semibold">NRP Project Access</h2>
          <p className="text-sm text-gray-500 mt-1">
            NRP access can be enabled by either a site administrator, or a project administrator with a linked NRP identity.
          </p>
        </div>

        <div className="flex items-center justify-between gap-4 rounded border px-4 py-3">
          <div>
            <p className="text-sm font-medium text-gray-900">Status: {config?.access_enabled ? 'Enabled' : 'Disabled'}</p>
            {config?.access_enabled_at && (
              <p className="text-xs text-gray-500 mt-0.5">
                {config.access_enabled ? 'Enabled' : 'Updated'} by {config.access_enabled_by_name || 'unknown'} on {new Date(config.access_enabled_at).toLocaleString()}
              </p>
            )}
          </div>
          {canManageProjectAccess && (
            <button
              type="button"
              disabled={updateMutation.isPending}
              onClick={() => updateMutation.mutate({ access_enabled: !config?.access_enabled })}
              className={`px-3 py-1.5 text-sm rounded ${config?.access_enabled ? 'bg-gray-200 text-gray-700 hover:bg-gray-300' : 'bg-brand-600 text-white hover:bg-brand-700'} disabled:opacity-50`}
            >
              {config?.access_enabled ? 'Disable NRP Access' : 'Enable NRP Access'}
            </button>
          )}
        </div>

        {!canManageProjectAccess && (
          <p className="text-xs text-gray-400">To change project-level NRP access, use a global admin account or link NRP as a project admin.</p>
        )}
      </div>

      <div className="bg-white p-6 rounded-lg border space-y-3">
        <div>
          <h2 className="text-lg font-semibold">NRP Execution</h2>
          <p className="text-sm text-gray-500 mt-1">
            Once NRP access is enabled for the project, project administrators with a linked NRP identity can enable execution on NRP.
          </p>
        </div>

        {!config?.access_enabled ? (
          <div className="rounded border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
            NRP access is not enabled for this project yet.
          </div>
        ) : !isProjectAdmin ? (
          <div className="rounded border px-4 py-3 text-sm text-gray-600">
            You can view the current NRP execution state, but only project administrators can change it.
          </div>
        ) : !linkStatus?.linked ? (
          <div className="rounded border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
            Link your NRP identity to manage NRP execution for this project.
          </div>
        ) : (
          <div className="space-y-4">
            <div className="flex items-center justify-between gap-4 rounded border px-4 py-3">
              <div>
                <p className="text-sm font-medium text-gray-900">Execution: {config?.execution_enabled ? 'Enabled' : 'Disabled'}</p>
                {config?.execution_enabled_at && (
                  <p className="text-xs text-gray-500 mt-0.5">
                    {config.execution_enabled ? 'Enabled' : 'Updated'} by {config.execution_enabled_by_name || 'unknown'} on {new Date(config.execution_enabled_at).toLocaleString()}
                  </p>
                )}
              </div>
              <button
                type="button"
                disabled={updateMutation.isPending}
                onClick={() => updateMutation.mutate({ execution_enabled: !config?.execution_enabled })}
                className={`px-3 py-1.5 text-sm rounded ${config?.execution_enabled ? 'bg-gray-200 text-gray-700 hover:bg-gray-300' : 'bg-brand-600 text-white hover:bg-brand-700'} disabled:opacity-50`}
              >
                {config?.execution_enabled ? 'Disable Execution on NRP' : 'Enable Execution on NRP'}
              </button>
            </div>

            <div className="rounded border border-dashed px-4 py-3 bg-gray-50">
              <div className="flex items-center justify-between gap-4">
                <div>
                  <p className="text-sm font-medium text-gray-900">Install NRP LLM Key</p>
                  <p className="text-xs text-gray-500 mt-0.5">
                    This will use your linked NRP access token to request a project-scoped NRP LLM key once the upstream API is deployed.
                  </p>
                </div>
                <button
                  type="button"
                  disabled
                  className="px-3 py-1.5 text-sm rounded bg-gray-200 text-gray-500 cursor-not-allowed"
                >
                  Install NRP LLM Key
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}