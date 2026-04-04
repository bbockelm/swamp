'use client';

import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { api } from '@/lib/api';

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const { data: session, isLoading } = useQuery({
    queryKey: ['session'],
    queryFn: api.auth.me,
  });

  if (isLoading) {
    return <div className="p-4 text-gray-400">Loading...</div>;
  }

  const isAdmin = session?.roles?.includes('admin');

  if (!isAdmin) {
    return (
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
        <div className="bg-white rounded-lg shadow-xl p-8 max-w-sm w-full mx-4 text-center">
          <div className="text-4xl mb-4">🔒</div>
          <h2 className="text-xl font-bold mb-2">Access Denied</h2>
          <p className="text-sm text-gray-600 mb-6">
            You do not have administrator privileges to view this page.
          </p>
          <Link
            href="/"
            className="inline-block px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 text-sm"
          >
            Go to Dashboard
          </Link>
        </div>
      </div>
    );
  }

  return <>{children}</>;
}
