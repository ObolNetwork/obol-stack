/**
 * Pure routing helpers for the top nav. No React, no Next runtime imports —
 * lives in its own module so node:test can exercise the mapping without
 * pulling next/navigation (which isn't resolvable from node's bare ESM
 * loader).
 */

import type { TopNavId } from '@/components/organisms/TopNav';

/**
 * Map a pathname onto the active TopNav tab. `/marketplace`,
 * `/marketplace/<slug>` and anything below all map to `marketplace`.
 * Same for `/agents`. Default is `home`. A bare `/marketplaceX` segment
 * still resolves to `home` because we only match `/marketplace`,
 * `/marketplace/...`.
 */
export function pathnameToTopNavId(pathname: string | null): TopNavId {
  if (!pathname) return 'home';
  if (pathname === '/marketplace' || pathname.startsWith('/marketplace/'))
    return 'marketplace';
  if (pathname === '/agents' || pathname.startsWith('/agents/'))
    return 'agents';
  return 'home';
}

/**
 * Map a TopNav id onto the canonical destination path. Centralised so the
 * routing target stays in one place; today these are 1:1 with the segment,
 * but if we ever want `/agents/instances` to be the agent landing the
 * change happens here.
 */
export function topNavIdToPath(id: TopNavId): string {
  switch (id) {
    case 'marketplace':
      return '/marketplace';
    case 'agents':
      return '/agents';
    case 'home':
    default:
      return '/';
  }
}
