'use client';

import type { ReactNode } from 'react';

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
 */
export default function MarketplaceLayout({
  children,
}: {
  children: ReactNode;
}) {
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
