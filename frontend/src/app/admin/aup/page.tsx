'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';

export default function AdminAUPPage() {
  const queryClient = useQueryClient();
  const { data: config, isLoading } = useQuery({
    queryKey: ['admin', 'aup'],
    queryFn: api.admin.getAUPConfig,
  });

  const [version, setVersion] = useState<string | undefined>(undefined);
  const [text, setText] = useState<string | undefined>(undefined);
  const [showUsers, setShowUsers] = useState(false);

  const currentVersion = version ?? config?.version ?? '';
  const currentText = text ?? config?.text ?? '';

  const updateMut = useMutation({
    mutationFn: (data: { version?: string; text?: string }) =>
      api.admin.updateAUPConfig(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'aup'] });
      queryClient.invalidateQueries({ queryKey: ['session'] });
    },
  });

  if (isLoading) return <div className="text-gray-400 text-sm">Loading...</div>;

  const agreed = config?.agreed ?? 0;
  const total = config?.total_users ?? 0;
  const pending = total - agreed;
  const pct = total > 0 ? Math.round((agreed / total) * 100) : 0;

  return (
    <div className="max-w-3xl space-y-8">
      <div>
        <h1 className="text-2xl font-bold">Acceptable Use Policy</h1>
        <p className="text-sm text-gray-500">
          Manage the AUP version and text. Bumping the version requires all
          users to re-agree before accessing the platform.
        </p>
      </div>

      {/* Agreement Stats */}
      <div className="bg-white p-6 rounded-lg border space-y-4">
        <h2 className="font-semibold text-lg">Agreement Status</h2>
        <div className="grid grid-cols-3 gap-4 text-center">
          <div className="bg-gray-50 rounded border p-3">
            <div className="text-2xl font-bold text-green-600">{agreed}</div>
            <div className="text-xs text-gray-500">Agreed</div>
          </div>
          <div className="bg-gray-50 rounded border p-3">
            <div className="text-2xl font-bold text-amber-600">{pending}</div>
            <div className="text-xs text-gray-500">Pending</div>
          </div>
          <div className="bg-gray-50 rounded border p-3">
            <div className="text-2xl font-bold text-gray-700">{total}</div>
            <div className="text-xs text-gray-500">Total Users</div>
          </div>
        </div>

        {/* Progress bar */}
        <div className="space-y-1">
          <div className="flex justify-between text-xs text-gray-500">
            <span>Compliance</span>
            <span>{pct}%</span>
          </div>
          <div className="h-2 bg-gray-200 rounded-full overflow-hidden">
            <div
              className="h-full bg-green-500 rounded-full transition-all"
              style={{ width: `${pct}%` }}
            />
          </div>
        </div>

        {/* User list toggle */}
        <button
          onClick={() => setShowUsers(!showUsers)}
          className="text-sm text-blue-600 hover:underline"
        >
          {showUsers ? 'Hide user details' : 'Show user details'}
        </button>

        {showUsers && config?.users && (
          <div className="border rounded overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 border-b">
                <tr>
                  <th className="text-left px-3 py-2 font-medium">User</th>
                  <th className="text-left px-3 py-2 font-medium">Email</th>
                  <th className="text-left px-3 py-2 font-medium">Status</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {config.users.map((u) => (
                  <tr key={u.user_id}>
                    <td className="px-3 py-2">{u.display_name}</td>
                    <td className="px-3 py-2 text-gray-500">{u.email}</td>
                    <td className="px-3 py-2">
                      {u.agreed_at ? (
                        <span className="inline-flex items-center gap-1 text-green-700 text-xs font-medium">
                          <span className="w-1.5 h-1.5 bg-green-500 rounded-full" />
                          Agreed {new Date(u.agreed_at).toLocaleDateString()}
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-amber-700 text-xs font-medium">
                          <span className="w-1.5 h-1.5 bg-amber-500 rounded-full" />
                          Pending
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* AUP Configuration */}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          updateMut.mutate({ version: currentVersion, text: currentText });
        }}
        className="bg-white p-6 rounded-lg border space-y-4"
      >
        <h2 className="font-semibold text-lg">Policy Configuration</h2>

        <div className="p-3 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
          <strong>Warning:</strong> Changing the version number will require all
          users to re-agree to the AUP before they can access the platform.
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            AUP Version
          </label>
          <input
            type="text"
            value={currentVersion}
            onChange={(e) => setVersion(e.target.value)}
            className="w-full border rounded-md px-3 py-2 text-sm"
            placeholder="1.0"
          />
          <p className="text-xs text-gray-400 mt-1">
            Current version: <strong>{config?.version}</strong>
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Policy Text
          </label>
          <textarea
            value={currentText}
            onChange={(e) => setText(e.target.value)}
            rows={10}
            className="w-full border rounded-md px-3 py-2 text-sm font-mono"
            placeholder="Enter the full Acceptable Use Policy text here. Leave blank to use the default built-in text."
          />
          <p className="text-xs text-gray-400 mt-1">
            This text is shown to users when they agree to the AUP. Leave blank
            to use the built-in default.
          </p>
        </div>

        <div className="flex items-center gap-4">
          <button
            type="submit"
            disabled={updateMut.isPending}
            className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {updateMut.isPending ? 'Saving...' : 'Save AUP Settings'}
          </button>
          {updateMut.isSuccess && (
            <span className="text-green-600 text-sm">Saved!</span>
          )}
          {updateMut.isError && (
            <span className="text-red-600 text-sm">
              Error: {updateMut.error?.message}
            </span>
          )}
        </div>
      </form>
    </div>
  );
}
