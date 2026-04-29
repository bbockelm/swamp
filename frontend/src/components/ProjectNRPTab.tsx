'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { NRPLLMKeyInstaller, useNRPLinkSession } from '@/components/NRPLLMKeyInstaller';

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
  const { linkStatus, linkingNRP, linkMessage, startLink } = useNRPLinkSession();

  const { data: config, isLoading } = useQuery({
    queryKey: ['project-nrp-config', projectId],
    queryFn: () => api.nrp.getProjectConfig(projectId),
  });

  const linked = !!linkStatus?.linked;
  const tokenHealthy = linkStatus?.token_healthy !== false;

  // Single toggle that flips both access_enabled and execution_enabled
  // atomically. Site admins can flip the project on without a linked NRP
  // identity (they're pre-approving). Project admins must have linked
  // and have a healthy NRP session.
  const toggleMutation = useMutation({
    mutationFn: (enabled: boolean) =>
      api.nrp.updateProjectConfig(projectId, {
        access_enabled: enabled,
        execution_enabled: enabled,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['project-nrp-config', projectId] });
    },
  });

  const isEnabled = !!config?.execution_enabled;
  const lastChangedAt = config?.execution_enabled_at ?? config?.access_enabled_at;
  const lastChangedBy = config?.execution_enabled_by_name ?? config?.access_enabled_by_name;

  // Permissions for the toggle. Site admins always allowed. Project
  // admins must have a linked, healthy NRP identity.
  const projectAdminCanToggle = isProjectAdmin && linked && tokenHealthy;
  const canToggle = isSystemAdmin || projectAdminCanToggle;

  // Reason text for why a project admin can't currently toggle. We only
  // show this for project admins (site admins always can).
  let toggleHint: string | null = null;
  if (!canToggle && isProjectAdmin) {
    if (!linkStatus?.oauth_configured) {
      toggleHint = 'NRP integration is not configured on this server.';
    } else if (!linked) {
      toggleHint = 'Link your NRP account to enable NRP for this project.';
    } else if (!tokenHealthy) {
      toggleHint = 'Your NRP session has expired — re-authenticate to manage NRP access.';
    }
  } else if (!canToggle && !isProjectAdmin) {
    toggleHint = 'A site administrator or a project administrator with a linked NRP identity can enable NRP for this project.';
  }

  if (isLoading) {
    return <p className="text-sm text-gray-500">Loading…</p>;
  }

  return (
    <div className="space-y-6">
      {linkMessage && (
        <div
          className={`border rounded-lg px-4 py-3 text-sm ${
            linkMessage.type === 'success'
              ? 'bg-green-50 border-green-200 text-green-800'
              : 'bg-red-50 border-red-200 text-red-800'
          }`}
        >
          {linkMessage.text}
        </div>
      )}

      {/* Primary toggle card. Linked-identity status, permissions
          explainer, and any link / re-auth actions all live here so the
          user has one place to look for "why can / can't I do this?". */}
      <div className="bg-white p-6 rounded-lg border space-y-4">
        <div>
          <h2 className="text-lg font-semibold">NRP for this project</h2>
          <p className="text-sm text-gray-500 mt-1">
            Allow this project to run analyses and request LLM keys against the National
            Research Platform. A site administrator or a project administrator with a linked
            NRP identity can enable this.
          </p>
        </div>

        <div className="flex items-center justify-between gap-4 rounded border px-4 py-3">
          <div>
            <p className="text-sm font-medium text-gray-900">
              Status: {isEnabled ? 'Enabled' : 'Disabled'}
            </p>
            {lastChangedAt && (
              <p className="text-xs text-gray-500 mt-0.5">
                {isEnabled ? 'Enabled' : 'Updated'} by {lastChangedBy || 'unknown'} on{' '}
                {new Date(lastChangedAt).toLocaleString()}
              </p>
            )}
          </div>
          {canToggle && (
            <button
              type="button"
              disabled={toggleMutation.isPending}
              onClick={() => toggleMutation.mutate(!isEnabled)}
              className={`px-3 py-1.5 text-sm rounded ${
                isEnabled
                  ? 'bg-gray-200 text-gray-700 hover:bg-gray-300'
                  : 'bg-brand-600 text-white hover:bg-brand-700'
              } disabled:opacity-50`}
            >
              {toggleMutation.isPending
                ? 'Saving…'
                : isEnabled
                ? 'Disable NRP'
                : 'Enable NRP'}
            </button>
          )}
        </div>

        {/* Linked-identity status + can-enable explainer. */}
        <div className="rounded border bg-gray-50 px-4 py-3 text-xs text-gray-700 space-y-2">
          {!linkStatus?.oauth_configured ? (
            <p>NRP integration is not configured on this server.</p>
          ) : !linked ? (
            <div className="flex items-center justify-between gap-3">
              <span>You have not linked an NRP account.</span>
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
            <div className="flex items-center justify-between gap-3">
              <span>
                Linked as <span className="font-medium">{linkStatus.nrp_login}</span> — session
                expired.
              </span>
              <button
                type="button"
                onClick={() => startLink()}
                disabled={linkingNRP}
                className="bg-brand-600 text-white px-3 py-1 text-xs rounded hover:bg-brand-700 disabled:opacity-50 whitespace-nowrap"
              >
                {linkingNRP ? 'Re-authenticating…' : 'Re-authenticate'}
              </button>
            </div>
          ) : (
            <p className="flex items-center gap-1.5">
              <svg className="w-3.5 h-3.5 text-green-600" fill="currentColor" viewBox="0 0 16 16">
                <path d="M6.173 14.727L.466 9.02l1.414-1.414 4.293 4.293L14.12-.049l1.414 1.414z" />
              </svg>
              Linked as <span className="font-medium">{linkStatus.nrp_login}</span>
            </p>
          )}
          {toggleHint && <p className="text-gray-500">{toggleHint}</p>}
        </div>

        {toggleMutation.isError && (
          <p className="text-xs text-red-600">
            {toggleMutation.error instanceof Error
              ? toggleMutation.error.message
              : 'Failed to update NRP settings.'}
          </p>
        )}
      </div>

      {/* LLM Key card is a peer of the toggle card, only shown when NRP
          is enabled for the project. The installer manages its own
          link/re-auth UI internally. */}
      {isEnabled && <NRPLLMKeyInstaller projectId={projectId} />}
    </div>
  );
}
