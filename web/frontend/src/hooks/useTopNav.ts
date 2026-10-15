'use client';

import { useCallback } from 'react';
import { usePathname, useRouter } from 'next/navigation';

import type { TopNavId } from '@/components/organisms/TopNav';
import { pathnameToTopNavId, topNavIdToPath } from './top-nav-route';

export interface UseTopNavResult {
  activeNav: TopNavId;
  onNavSelect: (id: TopNavId) => void;
}

export function useTopNav(): UseTopNavResult {
  const pathname = usePathname();
  const router = useRouter();
  const activeNav = pathnameToTopNavId(pathname);
  const onNavSelect = useCallback(
    (id: TopNavId) => {
      router.push(topNavIdToPath(id));
    },
    [router]
  );
  return { activeNav, onNavSelect };
}
