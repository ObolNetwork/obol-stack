'use client';

import {
  Box,
  Button,
  Flex,
  Link,
  OpenInNew,
  styled,
} from '@obolnetwork/obol-ui';
import { ClientOnly } from '@/components/atoms/ClientOnly';
import { ObolWordmark } from '@/components/atoms/ObolWordmark';
import { focusVisible } from '@/lib/ui/theme';
import { tones } from '@/lib/ui/tones';
import { WalletConnection } from '../../molecules/WalletConnection';
import { TopNav, type TopNavId } from '../TopNav';
import { publicStorefrontUrl } from '@/lib/services/public-storefront';
import { useTunnelURL } from '../../../hooks/useAgents';

// 3-column grid keeps the nav dead-center regardless of the left wordmark
// width vs. the right (storefront + wallet) cluster width. A flex
// space-between layout shifts the nav off-center when the sides are unequal,
// which they always are here.
const HeaderGrid = styled('div', {
  display: 'grid',
  gridTemplateColumns: '1fr auto 1fr',
  alignItems: 'center',
  gap: '$lg',
  width: '100%',

  // Phones: wordmark + actions on the first row, nav underneath.
  '@sm': {
    gridTemplateColumns: '1fr auto',
    rowGap: '$sm',
    columnGap: '$sm',
  },
});

const StorefrontLink = styled(Link, {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxxs',
  fontSize: '$2',
  color: '$obolGreen',
  textDecoration: 'none',
  border: `1px solid ${tones.success.border}`,
  borderRadius: '$pill',
  padding: '$xxxs $sm',
  transition: 'border-color 0.15s ease, background 0.15s ease',
  whiteSpace: 'nowrap',

  '&:hover': {
    borderColor: '$obolGreen',
    background: tones.success.bg,
  },
  ...focusVisible,

  // No room beside the wallet button on phones; the link is secondary.
  '@media (max-width: 480px)': { display: 'none' },
});

export interface DashboardHeaderProps {
  activeNav?: TopNavId;
  onNavSelect?: (id: TopNavId) => void;
}

export function DashboardHeader({
  activeNav = 'home',
  onNavSelect,
}: DashboardHeaderProps = {}) {
  const { tunnelURL } = useTunnelURL();
  const storefrontUrl = publicStorefrontUrl(tunnelURL);

  return (
    <HeaderGrid>
      <Box css={{ justifySelf: 'start' }}>
        <ObolWordmark />
      </Box>

      <Box
        css={{
          justifySelf: 'center',
          '@sm': { gridColumn: '1 / -1', gridRow: 2, justifySelf: 'start' },
        }}
      >
        <TopNav active={activeNav} onSelect={onNavSelect} />
      </Box>

      <Flex
        css={{
          justifySelf: 'end',
          gap: '$sm',
          alignItems: 'center',
        }}
      >
        <StorefrontLink
          href={storefrontUrl}
          target="_blank"
          rel="noopener noreferrer"
          title={
            tunnelURL
              ? 'Open the public storefront'
              : 'Open the local controller storefront'
          }
        >
          Storefront
          <OpenInNew size="sm" />
        </StorefrontLink>
        {/* Wallet state (wagmi/RainbowKit) is browser-only; reserve the
            button's space during SSR so the header doesn't shift. */}
        <ClientOnly
          fallback={
            <Button variant="primary" disabled aria-busy="true">
              Connect Wallet
            </Button>
          }
        >
          <WalletConnection />
        </ClientOnly>
      </Flex>
    </HeaderGrid>
  );
}
