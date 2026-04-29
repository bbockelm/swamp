'use client';

import { useCallback, useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError, NRPInstallLLMKeyResponse, NRPLinkStatus } from '@/lib/api';

const NRP_REAUTH_CODE = 'nrp_reauth_required';

// useNRPLinkSession centralises NRP OAuth popup management. It exposes
// the current link status, a startLink() callback that opens the OAuth
// popup, and a transient `linkingNRP` flag for buttons. It also
// transparently handles the postMessage from the popup callback so
// `nrp-link-status` is refreshed when the link is renewed.
export function useNRPLinkSession() {
  const queryClient = useQueryClient();
  const [linkingNRP, setLinkingNRP] = useState(false);
  const [linkMessage, setLinkMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);

  const { data: linkStatus } = useQuery<NRPLinkStatus>({
    queryKey: ['nrp-link-status'],
    queryFn: () => api.nrp.getLinkStatus(),
    staleTime: 30_000,
  });

  useEffect(() => {
    const handler = (e: MessageEvent) => {
      if (e.origin !== window.location.origin) return;
      if (e.data?.type !== 'identity-link-result' || e.data?.provider !== 'nrp') return;
      setLinkingNRP(false);
      if (e.data?.status === 'error') {
        setLinkMessage({ type: 'error', text: e.data?.message || 'Failed to link NRP account.' });
        return;
      }
      setLinkMessage({ type: 'success', text: e.data?.message || 'NRP account linked.' });
      queryClient.invalidateQueries({ queryKey: ['nrp-link-status'] });
    };
    window.addEventListener('message', handler);
    return () => window.removeEventListener('message', handler);
  }, [queryClient]);

  const startLink = useCallback(async () => {
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
  }, []);

  return { linkStatus, linkingNRP, linkMessage, startLink };
}

// NRPLLMKeyInstaller is the self-contained card that walks a project
// admin through obtaining a project-scoped NRP LLM API key. It handles
// session expiration, the group dropdown, install/replace, and auto-
// retries the install after a successful re-auth.
export function NRPLLMKeyInstaller({ projectId }: { projectId: string }) {
  const queryClient = useQueryClient();
  const { linkStatus, linkingNRP, startLink } = useNRPLinkSession();

  const [selectedGroup, setSelectedGroup] = useState<string>('');
  const [message, setMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);
  const [pendingGroup, setPendingGroup] = useState<string | null>(null);

  const linked = !!linkStatus?.linked;
  const tokenHealthy = linkStatus?.token_healthy !== false;
  const enabledForGroups = linked && tokenHealthy;

  const { data: groupsResp, isLoading: groupsLoading, error: groupsError, refetch: refetchGroups } = useQuery({
    queryKey: ['nrp-llm-groups', projectId],
    queryFn: () => api.nrp.listLLMGroups(projectId),
    staleTime: 60_000,
    retry: false,
    enabled: enabledForGroups,
  });

  const { data: providerKeys } = useQuery({
    queryKey: ['provider-keys', projectId],
    queryFn: () => api.providerKeys.list(projectId),
    staleTime: 30_000,
  });
  const activeNRPKey = providerKeys?.find((k) => k.provider === 'nrp' && k.is_active);

  const groups = groupsResp?.groups ?? [];
  const effectiveGroup = selectedGroup || (groups.length === 1 ? groups[0] : '');

  const installMut = useMutation({
    mutationFn: (groupName: string): Promise<NRPInstallLLMKeyResponse> =>
      api.nrp.installLLMKey(projectId, groupName || undefined),
    onSuccess: (resp) => {
      setMessage({
        type: 'success',
        text: `Installed NRP LLM key for group "${resp.group_name}" (${resp.key_hint}).`,
      });
      queryClient.invalidateQueries({ queryKey: ['provider-keys', projectId] });
      queryClient.invalidateQueries({ queryKey: ['available-providers', projectId] });
    },
    onError: (err: Error, groupName: string) => {
      if (err instanceof ApiError && err.code === NRP_REAUTH_CODE) {
        setPendingGroup(groupName);
        setMessage({ type: 'error', text: 'NRP session expired — re-authenticate to continue.' });
        queryClient.invalidateQueries({ queryKey: ['nrp-link-status'] });
        startLink();
        return;
      }
      setMessage({ type: 'error', text: err.message || 'Failed to install NRP LLM key.' });
    },
  });

  // Auto-retry the install when re-authentication succeeds.
  useEffect(() => {
    if (!pendingGroup) return;
    const handler = (e: MessageEvent) => {
      if (e.origin !== window.location.origin) return;
      if (e.data?.type !== 'identity-link-result' || e.data?.provider !== 'nrp') return;
      if (e.data?.status === 'success') {
        const g = pendingGroup;
        setPendingGroup(null);
        setMessage(null);
        installMut.mutate(g);
      } else {
        setPendingGroup(null);
      }
    };
    window.addEventListener('message', handler);
    return () => window.removeEventListener('message', handler);
  }, [pendingGroup, installMut]);

  const handleInstall = () => {
    setMessage(null);
    installMut.mutate(effectiveGroup);
  };

  const installDisabled =
    installMut.isPending ||
    linkingNRP ||
    !!pendingGroup ||
    !linked ||
    !tokenHealthy ||
    groupsLoading ||
    !!groupsError ||
    groups.length === 0 ||
    !effectiveGroup;

  return (
    <div className="bg-white p-6 rounded-lg border space-y-3">
      <div>
        <h2 className="text-lg font-semibold">NRP LLM Key</h2>
        <p className="text-sm text-gray-500 mt-1">
          Exchange your linked NRP account for a project-scoped LLM API key. Once installed,
          the key is available to analyses on this project just like any other LLM provider.
        </p>
      </div>

      {activeNRPKey && (
        <div className="rounded border bg-gray-50 px-3 py-2 text-xs text-gray-700">
          <span className="font-medium text-gray-900">Currently installed:</span> {activeNRPKey.label}{' '}
          <span className="text-gray-400">({activeNRPKey.key_hint})</span>
        </div>
      )}

      {!linkStatus?.oauth_configured ? (
        <div className="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800">
          NRP integration is not configured on this server.
        </div>
      ) : !linked ? (
        <div className="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 flex items-center justify-between gap-3">
          <span>Link your NRP account to install an LLM key.</span>
          <button
            type="button"
            onClick={() => startLink()}
            disabled={linkingNRP}
            className="bg-brand-600 text-white px-3 py-1 text-xs rounded hover:bg-brand-700 disabled:opacity-50 whitespace-nowrap"
          >
            {linkingNRP ? 'Linking…' : 'Link NRP Account'}
          </button>
        </div>
      ) : !tokenHealthy ? (
        <div className="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 flex items-center justify-between gap-3">
          <span>Your NRP session has expired — re-authenticate to install or replace the LLM key.</span>
          <button
            type="button"
            onClick={() => startLink()}
            disabled={linkingNRP}
            className="bg-brand-600 text-white px-3 py-1 text-xs rounded hover:bg-brand-700 disabled:opacity-50 whitespace-nowrap"
          >
            {linkingNRP ? 'Re-authenticating…' : 'Re-authenticate'}
          </button>
        </div>
      ) : groupsLoading ? (
        <p className="text-xs text-gray-500">Looking up your NRP LLM groups…</p>
      ) : groupsError ? (
        <div className="rounded border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700">
          {groupsError instanceof ApiError && groupsError.code === NRP_REAUTH_CODE
            ? 'NRP session expired — re-authenticate to continue.'
            : groupsError instanceof Error
            ? groupsError.message
            : 'Failed to list NRP LLM groups.'}{' '}
          <button type="button" onClick={() => refetchGroups()} className="underline">
            Retry
          </button>
        </div>
      ) : groups.length === 0 ? (
        <div className="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800">
          Your NRP account is not a member of any LLM-eligible groups. Ask an NRP administrator
          to add you to a LiteLLM-enabled group.
        </div>
      ) : groups.length === 1 ? (
        <p className="text-xs text-gray-700">
          Group <span className="font-mono font-medium">{groups[0]}</span> will be used.
        </p>
      ) : (
        <div>
          <label className="block text-xs font-medium text-gray-700 mb-1">LLM group</label>
          <select
            value={effectiveGroup}
            onChange={(e) => setSelectedGroup(e.target.value)}
            className="w-full border rounded px-2 py-1.5 text-sm bg-white"
          >
            <option value="">Choose a group…</option>
            {groups.map((g) => (
              <option key={g} value={g}>
                {g}
              </option>
            ))}
          </select>
        </div>
      )}

      {message && (
        <div
          className={`rounded border px-3 py-2 text-xs ${
            message.type === 'success'
              ? 'bg-green-50 border-green-200 text-green-800'
              : 'bg-red-50 border-red-200 text-red-800'
          }`}
        >
          {message.text}
        </div>
      )}

      <div className="flex items-center justify-end">
        <button
          type="button"
          disabled={installDisabled}
          onClick={handleInstall}
          className="px-3 py-1.5 text-sm rounded bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {installMut.isPending
            ? 'Installing…'
            : pendingGroup
            ? 'Waiting for re-authentication…'
            : activeNRPKey
            ? 'Replace NRP LLM Key'
            : 'Install NRP LLM Key'}
        </button>
      </div>
    </div>
  );
}
