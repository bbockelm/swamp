'use client';

import { useState, useEffect, useRef } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, Backup } from '@/lib/api';

function NextBackupCountdown({ lastCompletedAt }: { lastCompletedAt: string | null }) {
  const { data: settings } = useQuery({
    queryKey: ['admin', 'backup-settings'],
    queryFn: api.admin.getBackupSettings,
  });
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 10000);
    return () => clearInterval(id);
  }, []);

  if (!settings || !settings.backup_frequency_hours) return null;

  const freqMs = settings.backup_frequency_hours * 3600 * 1000;

  if (!lastCompletedAt) {
    return (
      <div className="bg-blue-50 border border-blue-200 rounded-lg px-4 py-2 mb-4 text-sm text-blue-800">
        Next scheduled backup: <strong>imminent</strong> (no previous backup found)
      </div>
    );
  }

  const lastMs = new Date(lastCompletedAt).getTime();
  const nextMs = lastMs + freqMs;
  const remainMs = nextMs - now;

  if (remainMs <= 0) {
    return (
      <div className="bg-blue-50 border border-blue-200 rounded-lg px-4 py-2 mb-4 text-sm text-blue-800">
        Next scheduled backup: <strong>imminent</strong> (overdue)
      </div>
    );
  }

  const totalMin = Math.ceil(remainMs / 60000);
  let label: string;
  if (totalMin < 60) {
    label = `${totalMin}m`;
  } else {
    const h = Math.floor(totalMin / 60);
    const m = totalMin % 60;
    label = m > 0 ? `${h}h ${m}m` : `${h}h`;
  }

  return (
    <div className="bg-blue-50 border border-blue-200 rounded-lg px-4 py-2 mb-4 text-sm text-blue-800">
      Next scheduled backup in <strong>{label}</strong>{' '}
      <span className="text-brand-600">(every {settings.backup_frequency_hours}h)</span>
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (!bytes || bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(i > 0 ? 2 : 0)} ${units[i]}`;
}

function timeAgo(dateStr: string | null): string {
  if (!dateStr) return '—';
  const seconds = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
  if (seconds < 60) return 'just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(dateStr).toLocaleDateString();
}

function formatDuration(secs: number): string {
  if (!secs) return '—';
  if (secs < 60) return `${secs}s`;
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return `${m}m ${s}s`;
}

function StatusBadge({ status }: { status: string }) {
  const styles: Record<string, string> = {
    completed: 'bg-green-100 text-green-800',
    failed: 'bg-red-100 text-red-800',
    running: 'bg-blue-100 text-blue-800',
    pending: 'bg-yellow-100 text-yellow-800',
  };
  return (
    <span
      className={`inline-flex items-center gap-1.5 text-xs font-medium px-2.5 py-1 rounded-full ${styles[status] ?? 'bg-gray-100 text-gray-800'}`}
    >
      {status === 'running' && (
        <span className="w-2 h-2 rounded-full bg-blue-500 animate-pulse" />
      )}
      {status}
    </span>
  );
}

function KeyModal({
  title,
  keyValue,
  onClose,
}: {
  title: string;
  keyValue: string;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-lg mx-4 p-6">
        <h3 className="text-lg font-semibold mb-4">{title}</h3>
        <div className="bg-gray-100 rounded p-3 font-mono text-xs break-all select-all mb-4">
          {keyValue}
        </div>
        <p className="text-xs text-amber-600 mb-4">
          Store this key securely. It is required to decrypt the backup.
        </p>
        <div className="flex justify-end gap-2">
          <button
            onClick={() => {
              navigator.clipboard.writeText(keyValue);
              setCopied(true);
              setTimeout(() => setCopied(false), 2000);
            }}
            className="text-sm px-3 py-1.5 rounded border hover:bg-gray-50"
          >
            {copied ? 'Copied!' : 'Copy'}
          </button>
          <button
            onClick={onClose}
            className="text-sm px-3 py-1.5 rounded bg-brand-600 text-white hover:bg-brand-700"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

export default function AdminBackupsPage() {
  const queryClient = useQueryClient();
  const [keyModal, setKeyModal] = useState<{
    title: string;
    key: string;
  } | null>(null);
  const [showUploadModal, setShowUploadModal] = useState(false);

  const { data: backups, isLoading } = useQuery({
    queryKey: ['admin', 'backups'],
    queryFn: api.admin.listBackups,
  });

  // Auto-refresh: 2s when a backup is running, 10s otherwise
  const hasRunning = backups?.some((b) => b.status === 'running' || b.status === 'pending');
  useEffect(() => {
    const interval = setInterval(
      () => queryClient.invalidateQueries({ queryKey: ['admin', 'backups'] }),
      hasRunning ? 2000 : 10000
    );
    return () => clearInterval(interval);
  }, [hasRunning, queryClient]);

  const triggerBackup = useMutation({
    mutationFn: api.admin.triggerBackup,
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ['admin', 'backups'] }),
  });

  const deleteBackup = useMutation({
    mutationFn: (id: string) => api.admin.deleteBackup(id),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ['admin', 'backups'] }),
  });

  const deleteFailedBackups = useMutation({
    mutationFn: api.admin.deleteFailedBackups,
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ['admin', 'backups'] }),
  });

  const restoreBackup = useMutation({
    mutationFn: (id: string) => api.admin.restoreBackup(id),
    onSuccess: () => alert('Restore started successfully.'),
    onError: () => alert('Failed to start restore.'),
  });

  const showPerBackupKey = async (backup: Backup) => {
    try {
      const { key } = await api.admin.getPerBackupKey(backup.id);
      setKeyModal({ title: `Encryption Key — ${backup.filename}`, key });
    } catch {
      alert('Failed to retrieve encryption key');
    }
  };

  const showGeneralKey = async () => {
    try {
      const { key } = await api.admin.getGeneralBackupKey();
      setKeyModal({ title: 'General Backup Key', key });
    } catch {
      alert('Failed to retrieve general key');
    }
  };

  const lastSuccessful = backups?.find((b) => b.status === 'completed');
  const failedCount = backups?.filter((b) => b.status === 'failed').length ?? 0;

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
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 mb-6">
        <h1 className="text-2xl font-bold">Backups</h1>
        <div className="flex flex-wrap items-center gap-2">
          {failedCount > 0 && (
            <button
              onClick={() => deleteFailedBackups.mutate()}
              className="text-sm px-3 py-1.5 rounded border border-red-300 text-red-700 hover:bg-red-50"
            >
              Clear Failed ({failedCount})
            </button>
          )}
          <button
            onClick={() => setShowUploadModal(true)}
            className="text-sm px-3 py-1.5 rounded border hover:bg-gray-50"
          >
            Upload &amp; Restore
          </button>
          <button
            onClick={showGeneralKey}
            className="text-sm px-3 py-1.5 rounded border hover:bg-gray-50"
          >
            General Key
          </button>
          <button
            onClick={() => triggerBackup.mutate()}
            disabled={triggerBackup.isPending}
            className="text-sm px-4 py-1.5 rounded bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50"
          >
            {triggerBackup.isPending ? 'Starting...' : 'Create Backup'}
          </button>
        </div>
      </div>

      {/* Summary bar */}
      {lastSuccessful && (
        <div className="bg-green-50 border border-green-200 rounded-lg px-4 py-2 mb-4 text-sm text-green-800">
          Last successful backup: <strong>{lastSuccessful.filename}</strong> —{' '}
          {timeAgo(lastSuccessful.completed_at)} ({formatBytes(lastSuccessful.size_bytes)})
        </div>
      )}

      {/* Next scheduled backup countdown */}
      <NextBackupCountdown lastCompletedAt={lastSuccessful?.completed_at ?? null} />

      {/* Table */}
      {!backups?.length ? (
        <p className="text-gray-500">No backups yet. Create one to get started.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b text-left text-xs text-gray-500 uppercase tracking-wide">
                <th className="py-2 pr-3">Filename</th>
                <th className="py-2 pr-3">Status</th>
                <th className="py-2 pr-3 hidden sm:table-cell">Size</th>
                <th className="py-2 pr-3 hidden sm:table-cell">Duration</th>
                <th className="py-2 pr-3 hidden md:table-cell">Started</th>
                <th className="py-2 pr-3 hidden md:table-cell">Encrypted</th>
                <th className="py-2">Actions</th>
              </tr>
            </thead>
            <tbody>
              {backups.map((b) => (
                <BackupRow
                  key={b.id}
                  backup={b}
                  onDelete={() => deleteBackup.mutate(b.id)}
                  onShowKey={() => showPerBackupKey(b)}
                  onRestore={() => {
                    if (confirm('Are you sure you want to restore from this backup? This will overwrite the current database.')) {
                      restoreBackup.mutate(b.id);
                    }
                  }}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Key modal */}
      {keyModal && (
        <KeyModal
          title={keyModal.title}
          keyValue={keyModal.key}
          onClose={() => setKeyModal(null)}
        />
      )}

      {/* Upload & Restore modal */}
      {showUploadModal && (
        <UploadRestoreModal onClose={() => setShowUploadModal(false)} />
      )}
    </div>
  );
}

function BackupRow({
  backup: b,
  onDelete,
  onShowKey,
  onRestore,
}: {
  backup: Backup;
  onDelete: () => void;
  onShowKey: () => void;
  onRestore: () => void;
}) {
  const [confirmDelete, setConfirmDelete] = useState(false);

  return (
    <tr className="border-b hover:bg-gray-50">
      <td className="py-2 pr-3">
        <span className="font-mono text-xs">{b.filename}</span>
        {b.error_msg && (
          <div className="text-xs text-red-600 mt-0.5 truncate max-w-xs" title={b.error_msg}>
            {b.error_msg}
          </div>
        )}
      </td>
      <td className="py-2 pr-3">
        <StatusBadge status={b.status} />
      </td>
      <td className="py-2 pr-3 text-gray-600 hidden sm:table-cell">
        {b.status === 'completed' ? formatBytes(b.size_bytes) : '—'}
      </td>
      <td className="py-2 pr-3 text-gray-600 hidden sm:table-cell">{formatDuration(b.duration_secs)}</td>
      <td className="py-2 pr-3 text-gray-500 hidden md:table-cell">{timeAgo(b.started_at)}</td>
      <td className="py-2 pr-3 hidden md:table-cell">
        {b.encrypted ? (
          <span className="text-green-600 text-xs">Yes</span>
        ) : (
          <span className="text-gray-400 text-xs">No</span>
        )}
      </td>
      <td className="py-2">
        <div className="flex items-center gap-1">
          {b.status === 'completed' && (
            <a
              href={api.admin.downloadBackupUrl(b.id)}
              className="text-xs px-2 py-1 rounded border hover:bg-gray-100"
              title="Download backup"
            >
              Download
            </a>
          )}
          {b.status === 'completed' && (
            <button
              onClick={onRestore}
              className="text-xs px-2 py-1 rounded border border-blue-300 text-blue-700 hover:bg-blue-50"
              title="Restore from this backup"
            >
              Restore
            </button>
          )}
          {b.encrypted && b.status === 'completed' && (
            <button
              onClick={onShowKey}
              className="text-xs px-2 py-1 rounded border hover:bg-gray-100"
              title="Show encryption key"
            >
              Key
            </button>
          )}
          {confirmDelete ? (
            <span className="flex items-center gap-1">
              <button
                onClick={() => {
                  onDelete();
                  setConfirmDelete(false);
                }}
                className="text-xs px-2 py-1 rounded bg-red-600 text-white hover:bg-red-700"
              >
                Confirm
              </button>
              <button
                onClick={() => setConfirmDelete(false)}
                className="text-xs px-2 py-1 text-gray-500 hover:text-gray-700"
              >
                Cancel
              </button>
            </span>
          ) : (
            <button
              onClick={() => setConfirmDelete(true)}
              className="text-xs px-2 py-1 rounded border border-red-300 text-red-700 hover:bg-red-50"
              title="Delete backup"
            >
              Delete
            </button>
          )}
        </div>
      </td>
    </tr>
  );
}

function UploadRestoreModal({ onClose }: { onClose: () => void }) {
  const fileRef = useRef<HTMLInputElement>(null);
  const [encrypted, setEncrypted] = useState(true);
  const [decryptKey, setDecryptKey] = useState('');
  const [status, setStatus] = useState<'idle' | 'uploading' | 'done' | 'error'>('idle');
  const [errorMsg, setErrorMsg] = useState('');

  const handleSubmit = async () => {
    const file = fileRef.current?.files?.[0];
    if (!file) return;
    setStatus('uploading');
    try {
      await api.admin.uploadRestore(file, encrypted, encrypted ? decryptKey || undefined : undefined);
      setStatus('done');
    } catch (err) {
      setStatus('error');
      setErrorMsg(err instanceof Error ? err.message : 'Upload failed');
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-md mx-4 p-6">
        <h3 className="text-lg font-semibold mb-4">Upload &amp; Restore</h3>

        {status === 'done' ? (
          <div>
            <p className="text-green-700 mb-4">Restore started successfully.</p>
            <button
              onClick={onClose}
              className="text-sm px-4 py-1.5 rounded bg-brand-600 text-white hover:bg-brand-700"
            >
              Close
            </button>
          </div>
        ) : (
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium mb-1">Backup file</label>
              <input
                ref={fileRef}
                type="file"
                accept=".tar.gz,.tgz,.enc"
                className="text-sm"
              />
            </div>

            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={encrypted}
                onChange={(e) => setEncrypted(e.target.checked)}
              />
              File is encrypted
            </label>

            {encrypted && (
              <div>
                <label className="block text-sm font-medium mb-1">Decryption key (hex)</label>
                <input
                  type="text"
                  value={decryptKey}
                  onChange={(e) => setDecryptKey(e.target.value)}
                  placeholder="Leave empty for general key"
                  className="w-full border rounded px-3 py-1.5 text-sm font-mono"
                />
              </div>
            )}

            {status === 'error' && (
              <p className="text-sm text-red-600">{errorMsg}</p>
            )}

            <div className="flex justify-end gap-2 pt-2">
              <button
                onClick={onClose}
                className="text-sm px-3 py-1.5 rounded border hover:bg-gray-50"
              >
                Cancel
              </button>
              <button
                onClick={handleSubmit}
                disabled={status === 'uploading'}
                className="text-sm px-4 py-1.5 rounded bg-brand-600 text-white hover:bg-brand-700 disabled:opacity-50"
              >
                {status === 'uploading' ? 'Uploading...' : 'Restore'}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
