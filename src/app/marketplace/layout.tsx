'use client';

import type { ReactNode } from 'react';
import { useEffect, useState } from 'react';
import { usePathname } from 'next/navigation';

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
  const pathname = usePathname();

  useEffect(() => {
    setHasMounted(true);
  }, []);

  if (!hasMounted) {
    return null;
  }

  // Only the browse grid needs the wider 1440px container; detail,
  // purchases, and listings keep the default 1280px page width.
  const wide = pathname === '/marketplace';

  return (
    <Providers>
      <DashboardTemplate
        wide={wide}
        header={<NavigatedDashboardHeader />}
        content={children}
        footer={<FooterBar />}
      />
    </Providers>
  );
}
