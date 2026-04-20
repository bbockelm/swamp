"use client";

import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type APIKey } from "@/lib/api";

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={() => {
        navigator.clipboard.writeText(text);
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
      }}
      className={`px-3 py-1 border rounded text-xs font-medium transition-colors ${
        copied
          ? "bg-green-100 border-green-300 text-green-700"
          : "bg-white hover:bg-gray-50"
      }`}
    >
      {copied ? "Copied!" : "Copy"}
    </button>
  );
}

export default function AdminAPIKeysPage() {
  const queryClient = useQueryClient();
  const { data: keys, isLoading } = useQuery({
    queryKey: ["api-keys"],
    queryFn: api.apiKeys.list,
  });

  const [showCreate, setShowCreate] = useState(false);
  const [keyName, setKeyName] = useState("");
  const [expiresIn, setExpiresIn] = useState("90d");
  const [newKey, setNewKey] = useState<string | null>(null);

  const createKey = useMutation({
    mutationFn: () =>
      api.apiKeys.create({ name: keyName, expires_in: expiresIn || undefined }),
    onSuccess: (result) => {
      setNewKey(result.key);
      setKeyName("");
      setExpiresIn("90d");
      setShowCreate(false);
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });

  const revokeKey = useMutation({
    mutationFn: (id: string) => api.apiKeys.revoke(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["api-keys"] }),
  });

  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null);

  if (isLoading) {
    return (
      <div className="animate-pulse space-y-3">
        {[...Array(3)].map((_, i) => (
          <div key={i} className="h-12 bg-gray-200 rounded" />
        ))}
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <div>
          <h1 className="text-2xl font-bold">API Keys</h1>
          <p className="text-sm text-gray-500 mt-1">
            Manage API keys for programmatic access to SWAMP.
          </p>
        </div>
        <button
          onClick={() => {
            setShowCreate(!showCreate);
            setNewKey(null);
          }}
          className={`px-4 py-2 rounded text-sm font-medium transition-colors ${
            showCreate
              ? "bg-gray-200 text-gray-700 hover:bg-gray-300"
              : "bg-brand-600 text-white hover:bg-brand-700"
          }`}
        >
          {showCreate ? "Cancel" : "+ New API Key"}
        </button>
      </div>

      {/* Inline create form */}
      {showCreate && (
        <div className="my-4 p-4 bg-gray-50 border rounded-lg">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              createKey.mutate();
            }}
            className="space-y-3"
          >
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
                  Key Name
                </label>
                <input
                  type="text"
                  value={keyName}
                  onChange={(e) => setKeyName(e.target.value)}
                  placeholder="e.g. CI/CD Pipeline"
                  required
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  autoFocus
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
                  Expiration
                </label>
                <select
                  value={expiresIn}
                  onChange={(e) => setExpiresIn(e.target.value)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                >
                  <option value="">No expiration</option>
                  <option value="1d">1 day</option>
                  <option value="7d">7 days</option>
                  <option value="30d">30 days</option>
                  <option value="90d">90 days</option>
                  <option value="365d">1 year</option>
                </select>
              </div>
            </div>
            <div className="flex gap-2">
              <button
                type="submit"
                disabled={createKey.isPending || !keyName.trim()}
                className="px-4 py-2 bg-brand-600 text-white rounded text-sm font-medium hover:bg-brand-700 disabled:opacity-50"
              >
                {createKey.isPending ? "Creating..." : "Create Key"}
              </button>
            </div>
            {createKey.isError && (
              <p className="text-sm text-red-600">
                Error: {createKey.error?.message || 'An unexpected error occurred'}
              </p>
            )}
          </form>
        </div>
      )}

      {/* New key success banner */}
      {newKey && (
        <div className="my-4 p-4 bg-green-50 border border-green-200 rounded-lg">
          <p className="font-semibold text-green-800 mb-2">
            API key created — copy it now, it won&apos;t be shown again:
          </p>
          <div className="flex items-center gap-2">
            <code className="flex-1 bg-white p-2 rounded border text-sm font-mono break-all select-all">
              {newKey}
            </code>
            <CopyButton text={newKey} />
          </div>
          <button
            onClick={() => setNewKey(null)}
            className="mt-2 text-xs text-green-700 hover:text-green-900"
          >
            Dismiss
          </button>
        </div>
      )}

      {/* Keys table */}
      {!keys?.length ? (
        <p className="text-gray-500 mt-4">No API keys.</p>
      ) : (
        <div className="overflow-x-auto mt-4">
          <table className="w-full border-collapse">
            <thead>
              <tr className="border-b text-left text-xs text-gray-500 uppercase tracking-wide">
                <th className="py-2 pr-4">Name</th>
                <th className="py-2 pr-4">Prefix</th>
                <th className="py-2 pr-4">Created</th>
                <th className="py-2 pr-4">Last Used</th>
                <th className="py-2 pr-4">Expires</th>
                <th className="py-2"></th>
              </tr>
            </thead>
            <tbody>
              {keys.map((k: APIKey) => (
                <tr key={k.id} className="border-b hover:bg-gray-50">
                  <td className="py-3 pr-4 font-medium">{k.name}</td>
                  <td className="py-3 pr-4">
                    <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded font-mono">
                      {k.key_prefix}...
                    </code>
                  </td>
                  <td className="py-3 pr-4 text-sm text-gray-500">
                    {new Date(k.created_at).toLocaleDateString()}
                  </td>
                  <td className="py-3 pr-4 text-sm text-gray-500">
                    {k.last_used_at
                      ? new Date(k.last_used_at).toLocaleDateString()
                      : "Never"}
                  </td>
                  <td className="py-3 pr-4 text-sm text-gray-500">
                    {k.expires_at
                      ? new Date(k.expires_at).toLocaleDateString()
                      : "Never"}
                  </td>
                  <td className="py-3 text-right">
                    {confirmRevoke === k.id ? (
                      <div className="flex items-center gap-2 justify-end">
                        <span className="text-xs text-red-600">Revoke?</span>
                        <button
                          onClick={() => {
                            revokeKey.mutate(k.id);
                            setConfirmRevoke(null);
                          }}
                          className="text-xs font-medium text-white bg-red-600 px-2 py-1 rounded hover:bg-red-700"
                        >
                          Yes
                        </button>
                        <button
                          onClick={() => setConfirmRevoke(null)}
                          className="text-xs text-gray-500 hover:text-gray-700"
                        >
                          No
                        </button>
                      </div>
                    ) : (
                      <button
                        onClick={() => setConfirmRevoke(k.id)}
                        className="text-xs text-red-600 hover:text-red-800 font-medium"
                      >
                        Revoke
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
