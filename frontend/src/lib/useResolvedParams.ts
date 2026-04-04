'use client';

import { useParams, usePathname } from 'next/navigation';

/**
 * Works around a Next.js static export limitation where useParams() returns
 * the generateStaticParams placeholder ('_') on direct page loads.
 *
 * Takes a route pattern like '/projects/[id]/analyses/[analysisId]' and
 * resolves each dynamic segment. Uses useParams() when it has real values,
 * otherwise matches against the URL pathname using the route pattern to
 * identify which segment corresponds to which parameter.
 */
export function useResolvedParams<T extends Record<string, string>>(
  pattern: string,
): T {
  const params = useParams();
  const pathname = usePathname();

  const patternParts = pattern.split('/').filter(Boolean);
  const pathParts = pathname.split('/').filter(Boolean);

  const resolved: Record<string, string> = {};

  for (let i = 0; i < patternParts.length; i++) {
    const match = patternParts[i].match(/^\[(.+)\]$/);
    if (match) {
      const name = match[1];
      const fromParams = params[name];
      resolved[name] =
        typeof fromParams === 'string' && fromParams !== '_'
          ? fromParams
          : pathParts[i] ?? '';
    }
  }

  return resolved as T;
}
