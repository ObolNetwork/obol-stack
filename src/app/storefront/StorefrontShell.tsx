'use client';

import type { ReactNode } from 'react';

import { DashboardTemplate } from '@/components/templates/DashboardTemplate';
import { NavigatedDashboardHeader } from '@/components/organisms/DashboardHeader';
import { FooterBar } from '@/components/organisms/FooterBar';
import { Providers } from '../providers';

export function StorefrontShell({
  children,
  fontClassName,
}: {
  children: ReactNode;
  fontClassName: string;
}) {
  return (
    <Providers>
      <DashboardTemplate
        wide
        header={<NavigatedDashboardHeader />}
        content={<div className={`shop-root ${fontClassName}`}>{children}</div>}
        footer={<FooterBar />}
      />
    </Providers>
  );
}
