'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, BackupSettings } from '@/lib/api';

export default function AdminSettingsPage() {
  return (
    <div className="max-w-3xl space-y-8">
      <div>
        <h1 className="text-2xl font-bold">Settings</h1>
        <p className="text-sm text-gray-500">System configuration for authentication and backups</p>
      </div>

      <OIDCConfigSection />
      <BackupConfigSection />
    </div>
  );
}

function OIDCConfigSection() {
  const queryClient = useQueryClient();
  const { data: config, isLoading } = useQuery({
    queryKey: ['admin', 'oidc-config'],
    queryFn: api.admin.getOIDCConfig,
  });

  const [form, setForm] = useState<{
    oidc_issuer: string;
    oidc_client_id: string;
    oidc_client_secret: string;
  } | null>(null);
  const [showSecret, setShowSecret] = useState(false);

  const currentForm = form ?? {
    oidc_issuer: config?.oidc_issuer ?? '',
    oidc_client_id: config?.oidc_client_id ?? '',
    oidc_client_secret: '',
  };

  const updateMut = useMutation({
    mutationFn: (data: { oidc_issuer?: string; oidc_client_id?: string; oidc_client_secret?: string }) => {
      const payload: Record<string, string> = {};
      if (data.oidc_issuer) payload.oidc_issuer = data.oidc_issuer;
      if (data.oidc_client_id) payload.oidc_client_id = data.oidc_client_id;
      if (data.oidc_client_secret) payload.oidc_client_secret = data.oidc_client_secret;
      return api.admin.updateOIDCConfig(payload);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'oidc-config'] });
    },
  });

  const callbackUrl = typeof window !== 'undefined'
    ? `${window.location.origin}/api/v1/auth/oidc/callback`
    : '';

  if (isLoading) return <div className="text-gray-400 text-sm">Loading OIDC config...</div>;

  return (
    <div className="bg-white p-6 rounded-lg border space-y-4">
      <h2 className="font-semibold text-lg">OIDC Authentication</h2>
      <p className="text-sm text-gray-500">
        Configure OpenID Connect for production authentication.
      </p>

      {callbackUrl && (
        <div className="p-3 bg-blue-50 rounded-md">
          <div className="text-xs font-medium text-blue-700 mb-1">Callback URL</div>
          <code className="text-xs text-blue-900 break-all">{callbackUrl}</code>
          <p className="text-xs text-blue-600 mt-1">
            Register this URL with your OIDC provider as an allowed redirect URI.
          </p>
        </div>
      )}

      <form
        onSubmit={(e) => {
          e.preventDefault();
          updateMut.mutate(currentForm);
        }}
        className="space-y-4"
      >
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Issuer URL</label>
          <input
            type="url"
            value={currentForm.oidc_issuer}
            onChange={(e) =>
              setForm({ ...currentForm, oidc_issuer: e.target.value })
            }
            placeholder="https://cilogon.org"
            className="w-full border rounded-md px-3 py-2 text-sm"
          />
          {config?.oidc_issuer && (
            <p className="text-xs text-green-600 mt-0.5">Currently set: {config.oidc_issuer}</p>
          )}
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Client ID</label>
          <input
            type="text"
            value={currentForm.oidc_client_id}
            onChange={(e) =>
              setForm({ ...currentForm, oidc_client_id: e.target.value })
            }
            placeholder="your-client-id"
            className="w-full border rounded-md px-3 py-2 text-sm"
          />
          {config?.oidc_client_id && (
            <p className="text-xs text-green-600 mt-0.5">Currently set: {config.oidc_client_id}</p>
          )}
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Client Secret</label>
          <div className="relative">
            <input
              type={showSecret ? 'text' : 'password'}
              value={currentForm.oidc_client_secret}
              onChange={(e) =>
                setForm({ ...currentForm, oidc_client_secret: e.target.value })
              }
              placeholder={config?.secret_set ? '••••• (already set, leave blank to keep)' : 'your-client-secret'}
              className="w-full border rounded-md px-3 py-2 text-sm pr-16"
            />
            <button
              type="button"
              onClick={() => setShowSecret(!showSecret)}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-xs text-gray-500 hover:text-gray-700"
            >
              {showSecret ? 'Hide' : 'Show'}
            </button>
          </div>
        </div>

        <div className="flex items-center gap-4">
          <button
            type="submit"
            disabled={updateMut.isPending}
            className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {updateMut.isPending ? 'Saving...' : 'Save OIDC Settings'}
          </button>
          {updateMut.isSuccess && <span className="text-green-600 text-sm">Saved!</span>}
          {updateMut.isError && (
            <span className="text-red-600 text-sm">Error: {updateMut.error?.message}</span>
          )}
        </div>
      </form>
    </div>
  );
}

function BackupConfigSection() {
  const queryClient = useQueryClient();
  const { data: settings, isLoading } = useQuery({
    queryKey: ['admin', 'backup-settings'],
    queryFn: api.admin.getBackupSettings,
  });

  const [form, setForm] = useState<BackupSettings | null>(null);

  const currentForm: BackupSettings = form ?? {
    backup_frequency_hours: settings?.backup_frequency_hours ?? 0,
    backup_bucket: settings?.backup_bucket ?? '',
    backup_endpoint: settings?.backup_endpoint ?? '',
    backup_access_key: settings?.backup_access_key ?? '',
    backup_secret_key: settings?.backup_secret_key ?? '',
    backup_use_ssl: settings?.backup_use_ssl ?? true,
  };

  const updateMut = useMutation({
    mutationFn: (data: BackupSettings) => api.admin.updateBackupSettings(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'backup-settings'] });
    },
  });

  if (isLoading) return <div className="text-gray-400 text-sm">Loading backup config...</div>;

  return (
    <div className="bg-white p-6 rounded-lg border space-y-4">
      <h2 className="font-semibold text-lg">Automated Backups</h2>
      <p className="text-sm text-gray-500">
        Configure automatic backup schedule and optional alternate S3 storage.
        Backups are encrypted with a key derived from the master key.
      </p>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          updateMut.mutate(currentForm);
        }}
        className="space-y-4"
      >
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Backup Frequency (hours)</label>
          <input
            type="number"
            min={0}
            step={1}
            value={currentForm.backup_frequency_hours}
            onChange={(e) =>
              setForm({
                ...currentForm,
                backup_frequency_hours: parseInt(e.target.value) || 0,
              })
            }
            className="w-full border rounded-md px-3 py-2 text-sm"
          />
          <p className="text-xs text-gray-400 mt-0.5">
            Set to 0 to disable automatic backups. Recommended: 24 (daily).
          </p>
        </div>

        <div className="border-t pt-4">
          <h3 className="text-sm font-medium text-gray-700 mb-2">Alternate Backup S3 (optional)</h3>
          <p className="text-xs text-gray-400 mb-3">
            Leave empty to store backups in the default S3 bucket.
          </p>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Endpoint</label>
              <input
                type="text"
                value={currentForm.backup_endpoint}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_endpoint: e.target.value })
                }
                placeholder="s3.amazonaws.com"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Bucket</label>
              <input
                type="text"
                value={currentForm.backup_bucket}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_bucket: e.target.value })
                }
                placeholder="my-backup-bucket"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Access Key</label>
              <input
                type="text"
                value={currentForm.backup_access_key}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_access_key: e.target.value })
                }
                placeholder="Leave empty to use default"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Secret Key</label>
              <input
                type="password"
                value={currentForm.backup_secret_key}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_secret_key: e.target.value })
                }
                placeholder="Leave empty to use default"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
          </div>
          <div className="mt-3">
            <label className="flex items-center gap-2 text-sm text-gray-700">
              <input
                type="checkbox"
                checked={currentForm.backup_use_ssl}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_use_ssl: e.target.checked })
                }
                className="rounded border-gray-300"
              />
              Use SSL/TLS
            </label>
          </div>
        </div>

        <div className="flex items-center gap-4">
          <button
            type="submit"
            disabled={updateMut.isPending}
            className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {updateMut.isPending ? 'Saving...' : 'Save Backup Settings'}
          </button>
          {updateMut.isSuccess && <span className="text-green-600 text-sm">Saved!</span>}
          {updateMut.isError && (
            <span className="text-red-600 text-sm">Error: {updateMut.error?.message}</span>
          )}
        </div>
      </form>
    </div>
  );
}
