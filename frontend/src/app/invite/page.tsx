'use client';

import { useSearchParams, useRouter } from 'next/navigation';
import { useQuery, useMutation } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { Suspense } from 'react';

function AcceptInviteInner() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const token = searchParams.get('token') ?? '';

  const { data: inviteInfo, isLoading: infoLoading, error: infoError } = useQuery({
    queryKey: ['invite-info', token],
    queryFn: () => api.groups.inviteInfo(token),
    enabled: !!token,
    retry: false,
  });

  const mutation = useMutation({
    mutationFn: () => api.groups.acceptInvite(token),
    onSuccess: (result: { group_id: string }) => {
      router.push(`/groups/${result.group_id}`);
    },
  });

  if (!token) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="bg-white p-8 rounded-lg border shadow-sm max-w-md text-center">
          <h1 className="text-xl font-bold mb-2">Invalid Invite Link</h1>
          <p className="text-gray-500 text-sm">No invite token was provided.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center">
      <div className="bg-white p-8 rounded-lg border shadow-sm max-w-md text-center">
        <h1 className="text-xl font-bold mb-2">Group Invite</h1>

        {infoLoading ? (
          <p className="text-gray-400 text-sm mb-6">Loading invite details...</p>
        ) : infoError ? (
          <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded p-3 mb-4">
            {infoError?.message || 'This invite link is invalid or has expired.'}
          </div>
        ) : inviteInfo ? (
          <div className="mb-6">
            <p className="text-gray-500 text-sm">
              You&apos;ve been invited to join
            </p>
            <p className="text-lg font-semibold text-gray-900 mt-1">{inviteInfo.group_name}</p>
            <p className="text-xs text-gray-400 mt-1">
              as {inviteInfo.role === 'admin' ? 'an administrator' : 'a member'}
            </p>
          </div>
        ) : null}

        {mutation.isError && (
          <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded p-3 mb-4">
            {mutation.error?.message || 'Failed to accept invite'}
          </div>
        )}

        {mutation.isSuccess ? (
          <div className="text-sm text-green-700 bg-green-50 border border-green-200 rounded p-3">
            You&apos;ve joined the group! Redirecting...
          </div>
        ) : (
          <button
            onClick={() => mutation.mutate()}
            disabled={mutation.isPending || infoLoading || !!infoError}
            className="bg-brand-600 text-white px-6 py-2 rounded hover:bg-brand-700 disabled:opacity-50"
          >
            {mutation.isPending ? 'Joining...' : 'Accept Invite'}
          </button>
        )}
      </div>
    </div>
  );
}

export default function AcceptInvitePage() {
  return (
    <Suspense fallback={
      <div className="min-h-screen flex items-center justify-center">
        <div className="text-gray-400">Loading...</div>
      </div>
    }>
      <AcceptInviteInner />
    </Suspense>
  );
}
