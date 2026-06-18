'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../atoms/Card';
import { SectionTitle } from '../atoms/SectionTitle';
import { CountBadge } from '../atoms/CountBadge';
import { StatusIndicator } from '../atoms/StatusIndicator';
import { ChainIcon } from '../atoms/ChainIcon';
import type { ErpcStatusData } from '../../types/dashboard';
import {
  networkToChainName,
  networkToStatus,
} from '../../lib/utils/network-rpc';

interface HomeNetworksWidgetProps {
  erpcData: ErpcStatusData | null;
}

function aliasDisplay(alias: string): string {
  if (alias === 'mainnet') return 'Ethereum';
  if (alias === 'base-sepolia') return 'Base Sepolia';
  return alias.charAt(0).toUpperCase() + alias.slice(1);
}

const TileGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'repeat(3, 1fr)',
  gap: '$xs',
  '@media (max-width: 768px)': {
    gridTemplateColumns: '1fr',
  },
});

const Tile = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  padding: '$xs $sm',
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
});

export function HomeNetworksWidget({ erpcData }: HomeNetworksWidgetProps) {
  const networks = erpcData?.networks ?? [];
  const upstreams = erpcData?.upstreams ?? [];

  if (networks.length === 0) return null;

  return (
    <Card css={{ padding: '$lg' }}>
      <Flex css={{ alignItems: 'center', gap: '$xs', marginBottom: '$md' }}>
        <SectionTitle>Networks</SectionTitle>
        <CountBadge count={networks.length} />
      </Flex>

      <TileGrid>
        {networks.map(network => {
          const chainName = networkToChainName(network.alias);
          const status = networkToStatus(network.state);
          const upstreamCount = upstreams.filter(
            u => u.network === network.id
          ).length;

          return (
            <Tile key={network.id}>
              <StatusIndicator status={status} size="sm" />
              <Box css={{ flex: 1, minWidth: 0 }}>
                <Text
                  css={{
                    fontSize: '$3',
                    color: '$light',
                    fontWeight: '$medium',
                  }}
                >
                  {aliasDisplay(network.alias)}
                </Text>
                <Text css={{ fontSize: '$1', color: '$muted' }}>
                  {upstreamCount} upstream{upstreamCount !== 1 ? 's' : ''}{' '}
                  &middot; eRPC
                </Text>
              </Box>
              {chainName && <ChainIcon chain={chainName} size={22} />}
            </Tile>
          );
        })}
      </TileGrid>
    </Card>
  );
}
