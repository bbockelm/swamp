'use client';

import { useSearchParams } from 'next/navigation';
import { useEffect, useState, Suspense } from 'react';
import Link from 'next/link';
import { api } from '@/lib/api';

type Status = 'loading' | 'success' | 'error' | 'closing';

function SetupContent() {
  const params = useSearchParams();
  const installationId = params.get('installation_id');
  const setupAction = params.get('setup_action');

  // Compute initial state synchronously — only the async claim path starts as 'loading'.
  const needsClaim = !!installationId && !isNaN(parseInt(installationId, 10))
    && (setupAction === 'install' || setupAction === 'update');

  const [status, setStatus] = useState<Status>(() => {
    if (!installationId || isNaN(parseInt(installationId, 10))) return 'error';
    if (!needsClaim) return 'success'; // request/unknown — show immediately
    return 'loading';
  });
  const [message, setMessage] = useState(() => {
    if (!installationId) return 'Missing installation_id parameter.';
    if (isNaN(parseInt(installationId, 10))) return 'Invalid installation_id parameter.';
    if (!needsClaim) {
      return setupAction === 'request'
        ? 'Installation request sent to the organization admin. You can close this tab.'
        : 'Done. You can close this tab and return to SWAMP.';
    }
    return '';
  });

  useEffect(() => {
    if (!needsClaim) return;

    const id = parseInt(installationId!, 10);
    api.github.claimInstallation(id)
      .then(() => {
        setStatus('success');
        setMessage('GitHub App installed successfully! This window will close automatically.');
        setTimeout(() => {
          setStatus('closing');
          window.close();
          // If window.close() didn't work (cross-origin tab), show a manual message.
          setTimeout(() => {
            setStatus('success');
            setMessage('GitHub App installed successfully! You can close this tab and return to SWAMP.');
          }, 500);
        }, 1500);
      })
      .catch((err) => {
        // Claim failed — installation may still be valid, just not claimable.
        // This is non-fatal; the webhook will have already recorded it.
        setStatus('success');
        setMessage(
          err?.message?.includes('401') || err?.message?.includes('403')
            ? 'GitHub App installed. Please log in to SWAMP to use it.'
            : 'GitHub App installed successfully! You can close this tab and return to SWAMP.'
        );
      });
  }, [needsClaim, installationId]);

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="bg-white rounded-lg shadow-sm border p-8 max-w-md w-full text-center space-y-4">
        <div className="text-4xl">
          {status === 'loading' && '⏳'}
          {status === 'success' && '✅'}
          {status === 'closing' && '✅'}
          {status === 'error' && '❌'}
        </div>
        <h1 className="text-xl font-semibold text-gray-900">
          {status === 'loading' ? 'Setting up...' : status === 'error' ? 'Setup Error' : 'Setup Complete'}
        </h1>
        <p className="text-gray-600 text-sm">{message || 'Claiming the installation...'}</p>
        {status !== 'loading' && status !== 'closing' && (
          <Link
            href="/"
            className="inline-block mt-4 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 text-sm"
          >
            Go to SWAMP
          </Link>
        )}
      </div>
    </div>
  );
}

export default function GitHubSetupPage() {
  return (
    <Suspense fallback={
      <div className="min-h-screen flex items-center justify-center bg-gray-50">
        <p className="text-gray-500">Loading...</p>
      </div>
    }>
      <SetupContent />
    </Suspense>
  );
}
