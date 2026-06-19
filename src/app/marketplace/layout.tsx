'use client';

import type { ReactNode } from 'react';
import { useEffect, useState } from 'react';

import { DashboardTemplate } from '@/components/templates/DashboardTemplate';
import { NavigatedDashboardHeader } from '@/components/organisms/DashboardHeader';
import { FooterBar } from '@/components/organisms/FooterBar';
import { Providers } from '../providers';

/**
 * Shared shell for /marketplace and /marketplace/[slug]. Uses the same
 * <DashboardTemplate> the home page uses so the header chrome, page
 * padding ($4xl sides / $xl top), $bg01 background, and footer are
 * identical across routes — fixes the "header is different on the
 * marketplace page" regression.
 *
 * Delay rendering until mounted — same gate as src/app/page.tsx — so
 * wagmi/RainbowKit hooks are not evaluated during static prerender
 * (Docker build has no WalletConnect project id).
 */
export default function MarketplaceLayout({
  children,
}: {
  children: ReactNode;
}) {
  const [hasMounted, setHasMounted] = useState(false);

  useEffect(() => {
    setHasMounted(true);
  }, []);

  if (!hasMounted) {
    return null;
  }

  return (
    <Providers>
      <DashboardTemplate
        header={<NavigatedDashboardHeader />}
        content={children}
        footer={<FooterBar />}
      />
    </Providers>
  );
}
