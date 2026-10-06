'use client';

import { useState, useEffect } from 'react';
import { usePathname } from 'next/navigation';

interface UseAnchorHashOptions {
  /**
   * The hash to search for (e.g., '#agent')
   * @default '#agent'
   */
  hash?: string;
}

interface UseAnchorHashResult {
  /**
   * Whether the specified hash is present in the URL
   */
  hasHash: boolean;
}

/**
 * Hook to check if a specific anchor hash is present in the URL
 *
 * @param options - Configuration options
 * @returns Object containing whether the hash is present
 *
 * @example
 * ```tsx
 * const { hasHash } = useAnchorHash({ hash: '#agent' });
 * ```
 */
export function useAnchorHash(
  options: UseAnchorHashOptions = {}
): UseAnchorHashResult {
  const { hash = '#agent' } = options;
  const pathname = usePathname();

  // Check for hash in URL to auto-open sidebar
  // Hash fragments aren't available in Next.js App Router hooks, so we check client-side
  // Start false on both server and client so hydration matches; the effect
  // below syncs with window.location.hash right after mount.
  const [hasHash, setHasHash] = useState(false);

  // Listen to hash changes
  useEffect(() => {
    const handleHashChange = () => {
      setHasHash(window.location.hash === hash);
    };

    window.addEventListener('hashchange', handleHashChange);
    // Also check on mount/navigation changes
    handleHashChange();

    return () => window.removeEventListener('hashchange', handleHashChange);
  }, [hash, pathname]); // Re-check when pathname changes

  return {
    hasHash,
  };
}
