import type { Metadata } from 'next';
import { Suspense } from 'react';
import './globals.css';
import { Providers } from './providers';
import { AppShell } from '@/components/AppShell';
import { SiteFooter } from '@/components/SiteFooter';

export const metadata: Metadata = {
  title: 'SWAMP — Software Assurance Marketplace',
  description: 'AI-powered security analysis for Git repositories',
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="min-h-screen flex flex-col">
        <Providers>
          <div className="flex-1 flex flex-col">
            <Suspense fallback={<div className="min-h-screen flex items-center justify-center text-gray-400">Loading...</div>}>
              <AppShell>{children}</AppShell>
            </Suspense>
          </div>
          <SiteFooter />
        </Providers>
      </body>
    </html>
  );
}
