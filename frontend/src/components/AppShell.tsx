'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useState, useEffect } from 'react';
import { Sidebar } from './Sidebar';
import { RenderedMarkdown } from './MarkdownReport';
import { api } from '@/lib/api';

const publicPaths = ['/login', '/', '/github/setup', '/github/linked', '/tutorials/onboarding'];

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [agreeing, setAgreeing] = useState(false);
  const [displayName, setDisplayName] = useState<string | undefined>(undefined);

  const { data: session, isLoading } = useQuery({
    queryKey: ['session'],
    queryFn: api.auth.me,
  });

  const currentDisplayName = displayName ?? session?.user?.display_name ?? '';

  const agreeAup = useMutation({
    mutationFn: async () => {
      // Update display name if changed
      if (currentDisplayName && currentDisplayName !== session?.user?.display_name) {
        await api.auth.updateProfile(currentDisplayName);
      }
      await api.auth.agreeAup(session?.aup_version ?? '');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['session'] });
      setAgreeing(false);
    },
    onError: () => setAgreeing(false),
  });

  const isPublic = publicPaths.includes(pathname);
  const isAuthenticated = session?.authenticated && session?.user;
  const needsAup = isAuthenticated && !session?.aup_agreed;
  const roles = session?.roles ?? [];

  // Build full URL with query string for return_to
  const search = searchParams.toString();
  const fullPath = search ? `${pathname}?${search}` : pathname;

  // Redirect unauthenticated users to login (must be in useEffect to avoid setState during render)
  useEffect(() => {
    if (!isLoading && !isAuthenticated && !isPublic) {
      router.replace(`/login?return_to=${encodeURIComponent(fullPath)}`);
    }
  }, [isLoading, isAuthenticated, isPublic, fullPath, router]);

  // Don't gate public pages for unauthenticated users
  if (isPublic && !isAuthenticated) {
    return <>{children}</>;
  }

  // Show nothing while redirecting unauthenticated users
  if (!isLoading && !isAuthenticated) {
    return null;
  }

  // Show AUP modal over the normal layout when agreement is needed
  return (
    <div className="flex min-h-screen">
      <Sidebar roles={roles} userName={session?.user?.display_name || session?.user?.email} />
      <main className="flex-1 min-w-0 overflow-auto pt-16 px-4 pb-6 lg:pt-8 lg:px-8 lg:pb-8">
        {needsAup ? (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
            <div className="bg-white rounded-lg shadow-xl p-8 max-w-lg w-full mx-4">
              <h2 className="text-xl font-bold mb-4">Welcome to SWAMP</h2>
              <div className="mb-4">
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  Display Name
                </label>
                <input
                  type="text"
                  value={currentDisplayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  className="w-full border rounded px-3 py-2 text-sm"
                  placeholder="Your name"
                />
                <p className="text-xs text-gray-400 mt-1">
                  You can change this later in Settings.
                </p>
              </div>
              <div className="mb-6 text-sm text-gray-700 space-y-3">
                <p>
                  Before using SWAMP, you must agree to the Acceptable Use Policy
                  (version {session.aup_version}).
                </p>
                <div className="bg-gray-50 border rounded p-4 max-h-48 overflow-y-auto text-xs leading-relaxed prose prose-xs max-w-none">
                  <RenderedMarkdown content={session.aup_text || ''} />
                  <hr className="my-2 border-gray-200" />
                  <p className="text-[10px] text-gray-400 italic">
                    This is the acceptable use policy v{session.aup_version}.
                    {session.aup_updated_at && <> This text was last updated on {session.aup_updated_at}.</>}
                    {' '}It can be found at <a href={session.aup_url || '/aup'} target="_blank" rel="noopener noreferrer" className="text-brand-600 underline">{session.aup_url || '/aup'}</a>.
                  </p>
                </div>
              </div>
              <button
                onClick={() => {
                  setAgreeing(true);
                  agreeAup.mutate();
                }}
                disabled={agreeing}
                className="w-full bg-brand-600 text-white py-2 rounded hover:bg-brand-700 disabled:opacity-50"
              >
                {agreeing ? 'Submitting...' : 'I Agree'}
              </button>
              {agreeAup.isError && (
                <p className="mt-3 text-sm text-red-600">
                  Failed to record agreement. Please try again.
                </p>
              )}
            </div>
          </div>
        ) : (
          children
        )}
      </main>
    </div>
  );
}
