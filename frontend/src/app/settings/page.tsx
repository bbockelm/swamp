'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, type UserIdentity } from '@/lib/api';

export default function AdminSettingsPage() {
  const { data: session } = useQuery({
    queryKey: ['session'],
    queryFn: api.auth.me,
  });

  const { data: stats } = useQuery({
    queryKey: ['my-stats'],
    queryFn: api.auth.myStats,
    enabled: !!session?.user,
  });

  const { data: versionInfo } = useQuery({
    queryKey: ['version'],
    queryFn: api.version,
    staleTime: Infinity,
  });

  const { data: identities } = useQuery({
    queryKey: ['my-identities'],
    queryFn: api.auth.myIdentities,
    enabled: !!session?.user,
  });

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Info</h1>

      <div className="space-y-8 max-w-2xl">
        {/* Current user info */}
        <div>
          <h2 className="text-lg font-semibold mb-3">Your Account</h2>
          {session?.user ? (
            <div className="bg-gray-50 p-4 rounded border text-sm space-y-3">
              <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
                <span className="text-gray-500">Name:</span>
                <span>{session.user.display_name}</span>
                <span className="text-gray-500">Email:</span>
                <span>{session.user.email}</span>
                <span className="text-gray-500">Roles:</span>
                <span>{session.roles.join(', ') || 'none'}</span>
                {stats?.member_since && (
                  <>
                    <span className="text-gray-500">Member since:</span>
                    <span>{new Date(stats.member_since).toLocaleDateString()}</span>
                  </>
                )}
              </div>

              {stats && (
                <div className="border-t pt-3 mt-3">
                  <div className="grid grid-cols-5 gap-3 text-center">
                    <StatPill label="Projects" value={stats.project_count} />
                    <StatPill label="Groups" value={stats.group_count} />
                    <StatPill label="Packages" value={stats.package_count} />
                    <StatPill label="Analyses" value={stats.analysis_count} />
                    <StatPill label="Findings" value={stats.finding_count} />
                  </div>
                </div>
              )}
            </div>
          ) : (
            <p className="text-gray-500 text-sm">Not logged in.</p>
          )}
        </div>

        {/* AUP Agreement */}
        {session && !session.aup_agreed && (
          <AUPSection aupVersion={session.aup_version} />
        )}

        {/* Linked Identities */}
        {identities && identities.length > 0 && (
          <div>
            <h2 className="text-lg font-semibold mb-3">Linked Identities</h2>
            <div className="bg-gray-50 rounded border divide-y">
              {identities.map((identity) => (
                <div key={identity.id} className="p-4 text-sm">
                  <div className="flex items-center gap-2 mb-1">
                    <span className="font-medium">{identity.display_name || identity.email || identity.subject}</span>
                    {identity.idp_name && (
                      <span className="text-xs bg-gray-200 text-gray-600 px-1.5 py-0.5 rounded">
                        {identity.idp_name}
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-gray-500 space-y-0.5">
                    {identity.email && <div>{identity.email}</div>}
                    <div className="font-mono text-gray-400">{identity.issuer}</div>
                    <div className="text-gray-400">Linked {new Date(identity.created_at).toLocaleDateString()}</div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* API Documentation */}
        <div>
          <h2 className="text-lg font-semibold mb-3">API Documentation</h2>
          <div className="bg-gray-50 rounded border overflow-hidden">
            <div className="p-4 text-sm space-y-2">
              <p className="text-gray-600">
                Explore the full REST API using the interactive Swagger UI, or
                download the OpenAPI specification for code generation.
              </p>
              <div className="flex gap-3 pt-1">
                <a
                  href="/api/v1/docs"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 bg-brand-600 text-white px-4 py-2 rounded hover:bg-brand-700 text-sm font-medium"
                >
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                  </svg>
                  Open Swagger UI
                </a>
                <a
                  href="/api/v1/openapi.yaml"
                  className="inline-flex items-center gap-1.5 border border-gray-300 text-gray-700 px-4 py-2 rounded hover:bg-gray-100 text-sm font-medium"
                >
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
                  </svg>
                  Download OpenAPI Spec
                </a>
              </div>
            </div>
          </div>
        </div>

        {/* API Keys link */}
        {session?.roles?.includes('admin') && (
          <div>
            <h2 className="text-lg font-semibold mb-3">API Keys</h2>
            <div className="bg-gray-50 p-4 rounded border text-sm">
              <p className="text-gray-600 mb-2">
                Manage API keys for programmatic access to the SWAMP API.
              </p>
              <a
                href="/admin/api-keys"
                className="text-brand-600 hover:underline text-sm font-medium"
              >
                Manage API Keys →
              </a>
            </div>
          </div>
        )}

        {/* Version */}
        {versionInfo && (
          <div>
            <h2 className="text-lg font-semibold mb-3">Version</h2>
            <div className="bg-gray-50 p-4 rounded border text-sm">
              <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
                <span className="text-gray-500">Version:</span>
                <span className="font-mono">{versionInfo.version}</span>
                <span className="text-gray-500">Commit:</span>
                <span className="font-mono">{versionInfo.commit}</span>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function StatPill({ label, value }: { label: string; value: number }) {
  return (
    <div className="bg-white rounded border px-2 py-1.5">
      <div className="text-lg font-bold text-gray-900">{value}</div>
      <div className="text-xs text-gray-500">{label}</div>
    </div>
  );
}

function AUPSection({ aupVersion }: { aupVersion: string }) {
  const queryClient = useQueryClient();

  const mutation = useMutation({
    mutationFn: () => api.auth.agreeAup(aupVersion),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['session'] });
    },
  });

  return (
    <div className="bg-yellow-50 border border-yellow-200 p-4 rounded">
      <h2 className="text-lg font-semibold mb-2">Acceptable Use Policy</h2>
      <p className="text-sm text-gray-700 mb-4">
        You must agree to the Acceptable Use Policy before using SWAMP.
        By clicking below, you agree to:
      </p>
      <ul className="list-disc ml-6 text-sm text-gray-700 mb-4 space-y-1">
        <li>Use SWAMP only for authorized security analysis</li>
        <li>Not use analysis results for malicious purposes</li>
        <li>Respect the intellectual property of analyzed code</li>
        <li>Report any security issues found to the appropriate parties</li>
      </ul>
      <button
        onClick={() => mutation.mutate()}
        disabled={mutation.isPending}
        className="bg-brand-600 text-white px-4 py-2 rounded hover:bg-brand-700 disabled:opacity-50"
      >
        {mutation.isPending ? 'Agreeing...' : 'I Agree'}
      </button>
    </div>
  );
}
