import type { ReactNode } from 'react';

import { MarketplaceShell } from './MarketplaceShell';

/**
 * Shared shell for /marketplace and /marketplace/[slug]. Uses the same
 * <DashboardTemplate> the home page uses so the header chrome, page
 * padding, $bg01 background, and footer are identical across routes.
 *
 * Server-renders the shell; wallet-only UI is wrapped in <ClientOnly> where
 * it is used, and the wagmi config is SSR-safe (no connectors server-side).
 */
export default function MarketplaceLayout({
  children,
}: {
  children: ReactNode;
}) {
  return <MarketplaceShell>{children}</MarketplaceShell>;
}
