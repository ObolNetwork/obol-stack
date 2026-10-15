'use client';

import type { ReactNode } from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

import { ObolWordmark } from '@/components/atoms/ObolWordmark';
import { FooterBar } from '@/components/organisms/FooterBar';
import { TopNav } from '@/components/organisms/TopNav';
import { DashboardTemplate } from '@/components/templates/DashboardTemplate';
import { useTopNav } from '@/hooks/useTopNav';

const HeaderRow = styled('header', {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  flexWrap: 'wrap',
  gap: '$lg',
});

/**
 * Dashboard chrome without the wallet/data providers — used by not-found and
 * error boundaries so they still render if a provider is what failed.
 */
export function FallbackShell({ children }: { children: ReactNode }) {
  const { activeNav, onNavSelect } = useTopNav();
  return (
    <DashboardTemplate
      header={
        <HeaderRow>
          <Box as="a" href="/" aria-label="Obol Stack dashboard">
            <ObolWordmark />
          </Box>
          <TopNav active={activeNav} onSelect={onNavSelect} />
        </HeaderRow>
      }
      content={children}
      footer={<FooterBar />}
    />
  );
}
