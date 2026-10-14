'use client';

import type { ReactNode } from 'react';
import { usePathname } from 'next/navigation';

import { DashboardTemplate } from '@/components/templates/DashboardTemplate';
import { NavigatedDashboardHeader } from '@/components/organisms/DashboardHeader';
import { FooterBar } from '@/components/organisms/FooterBar';
import { Providers } from '../providers';

export function MarketplaceShell({ children }: { children: ReactNode }) {
  const pathname = usePathname();
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
