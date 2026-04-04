'use client';

import { useSearchParams, useRouter } from 'next/navigation';
import { useMutation } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { Suspense } from 'react';

function AcceptInviteInner() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const token = searchParams.get('token') ?? '';

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
        <p className="text-gray-500 text-sm mb-6">
          You&apos;ve been invited to join a group. Click below to accept.
        </p>

        {mutation.isError && (
          <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded p-3 mb-4">
            {(mutation.error as Error)?.message || 'Failed to accept invite'}
          </div>
        )}

        {mutation.isSuccess ? (
          <div className="text-sm text-green-700 bg-green-50 border border-green-200 rounded p-3">
            You&apos;ve joined the group! Redirecting...
          </div>
        ) : (
          <button
            onClick={() => mutation.mutate()}
            disabled={mutation.isPending}
            className="bg-blue-600 text-white px-6 py-2 rounded hover:bg-blue-700 disabled:opacity-50"
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
