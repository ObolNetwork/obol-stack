'use client';

import type { ReactNode } from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

import { NavigatedDashboardHeader } from '@/components/organisms/DashboardHeader';
import { Providers } from '../providers';

const Shell = styled(Box, {
  minHeight: '100vh',
  background: '#091011',
  display: 'flex',
  flexDirection: 'column',
});

const Main = styled(Box, {
  flex: 1,
  minHeight: 0,
});

export default function MarketplaceLayout({
  children,
}: {
  children: ReactNode;
}) {
  return (
    <Providers>
      <Shell>
        <NavigatedDashboardHeader />
        <Main>{children}</Main>
      </Shell>
    </Providers>
  );
}
