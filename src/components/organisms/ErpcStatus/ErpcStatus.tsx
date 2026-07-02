'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { useWalletClient } from 'wagmi';
import { tones } from '../../../lib/ui/tones';
import { ChainIcon, CardAppType, CardAppId } from '../../atoms/AppIcon';
import { Badge } from '../../atoms/Badge';
import { StatusPill } from '../../atoms/StatusIndicator';
import { CopyButton } from '../../atoms/CopyButton';
import { AddNetworkButton } from '../../molecules/AddNetworkButton';
import { Accordion } from '../../molecules/Accordion';
import { BaseSepoliaObolFaucet } from '../../molecules/BaseSepoliaObolFaucet';
import {
  ErpcNetwork,
  ErpcUpstream,
  LocalNodeData,
} from '../../../types/dashboard';
import { LocalNodeStatus } from '../LocalNodeStatus/LocalNodeStatus';
import { RemoteNodeStatus } from '../RemoteNodeStatus/RemoteNodeStatus';
import {
  networkToChainName,
  networkToStatus,
  chainIdFromEvmId,
  erpcUrl,
  TESTNETS,
} from '../../../lib/utils/network-rpc';

// ── Network card ──────────────────────────────────────────────────────────────

const NetworkCardWrapper = styled(Box, {
  borderRadius: '$2',
  background: '$bg03',
  border: '1px solid $stroke',
  overflow: 'hidden',
});

const NetworkCardHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$sm',
  padding: '$xs $sm',
});

// ── Details section ───────────────────────────────────────────────────────────

const DetailsSection = styled(Box, {
  borderTop: '1px solid $stroke',
  padding: '$xs $sm $sm',
  display: 'grid',
  gridTemplateColumns: '72px 1fr',
  columnGap: '$sm',
  rowGap: '$xxs',
  alignItems: 'baseline',
});

const DetailLabel = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '$wider',
  whiteSpace: 'nowrap',
});

const DetailValue = styled(Text, {
  fontSize: '$2',
  color: '$light',
  fontFamily: 'monospace',
  wordBreak: 'break-all',
  lineHeight: '$base',
});

const EndpointRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  minWidth: 0,
});

const TestnetChip = styled(Text, {
  fontSize: '10px',
  fontWeight: '$semibold',
  color: '$body',
  backgroundColor: tones.neutral.bg,
  border: `1px solid ${tones.neutral.border}`,
  borderRadius: '4px',
  padding: '1px 6px',
  lineHeight: 1.4,
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
  whiteSpace: 'nowrap',
});

// Sizing overrides so the local/remote source Badge sits flush with TestnetChip.
const sourceChipCss = {
  fontSize: '10px',
  fontWeight: '$semibold',
  borderRadius: '4px',
  padding: '1px 6px',
  lineHeight: 1.4,
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
  whiteSpace: 'nowrap',
} as const;

// ── Upstream cards ───────────────────────────────────────────────────────────

const UpstreamSection = styled(Box, {
  borderTop: '1px solid $stroke',
});

const UpstreamSectionLabel = styled(Text, {
  fontSize: '$2',
  fontWeight: '$semibold',
  color: '$body',
  textTransform: 'uppercase',
});

const UpstreamsGrid = styled(Box, {
  display: 'grid',
  gap: '$xs',
  gridTemplateColumns: '1fr',
  '@media (min-width: 640px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },
  paddingTop: '$lg',
});

function aliasDisplay(alias: string): string {
  if (alias === 'mainnet') return 'Ethereum';
  if (alias === 'base-sepolia') return 'Base Sepolia';
  return alias.charAt(0).toUpperCase() + alias.slice(1);
}

// ── ErpcStatus ────────────────────────────────────────────────────────────────

export function ErpcStatus({
  network,
  upstreams,
  localNodes,
}: {
  network: ErpcNetwork;
  upstreams?: ErpcUpstream[];
  localNodes?: LocalNodeData[];
}) {
  const { data: walletClient } = useWalletClient();
  const chainName = networkToChainName(network.alias);
  const status = networkToStatus(network.state);
  const chainId = chainIdFromEvmId(network.id);
  const isMainnet = chainIdFromEvmId(network.id) === 1;
  const isTestnet = TESTNETS.has(network.alias);
  const endpoint = `${erpcUrl()}/${network.alias}`;

  const erpcUpstreams = upstreams ?? [];
  const localUpstreams = localNodes ?? [];
  const totalUpstreams = erpcUpstreams.length + localUpstreams.length;
  // Local nodes are the primary serving path; remote upstreams are fallback.
  const hasLocalNode = localUpstreams.length > 0;

  return (
    <NetworkCardWrapper>
      <NetworkCardHeader>
        <Flex css={{ alignItems: 'center', gap: '$sm' }}>
          {chainName && <ChainIcon chain={chainName} size={24} />}
          <Flex css={{ flexDirection: 'column', gap: '$xxxs', width: 'auto' }}>
            <CardAppType>RPC</CardAppType>
            <Flex css={{ alignItems: 'center', gap: '$xs', width: 'auto' }}>
              <CardAppId css={hasLocalNode ? undefined : { color: '$body' }}>
                {aliasDisplay(network.alias)}
              </CardAppId>
              {isTestnet && <TestnetChip>Testnet</TestnetChip>}
              {hasLocalNode ? (
                <Box title="Backed by a locally-synced node">
                  <Badge
                    variant="green"
                    css={{
                      ...sourceChipCss,
                      border: `1px solid ${tones.success.border}`,
                    }}
                  >
                    Local
                  </Badge>
                </Box>
              ) : (
                <Box title="Served by remote upstreams only">
                  <Badge
                    variant="neutral"
                    css={{
                      ...sourceChipCss,
                      color: '$muted',
                      backgroundColor: tones.neutral.bg,
                      border: `1px solid ${tones.neutral.border}`,
                    }}
                  >
                    Remote
                  </Badge>
                </Box>
              )}
            </Flex>
          </Flex>
        </Flex>

        <Flex css={{ alignItems: 'center', gap: '$xxs', width: 'auto' }}>
          <AddNetworkButton
            chainName={chainName ?? (isMainnet ? 'mainnet' : 'sepolia')}
            walletClient={chainName ? walletClient : null}
          />
          <StatusPill status={status} />
        </Flex>
      </NetworkCardHeader>

      <DetailsSection>
        <DetailLabel>Endpoint</DetailLabel>
        <EndpointRow>
          <DetailValue>{endpoint}</DetailValue>
          <CopyButton text={endpoint} title="Copy endpoint" />
        </EndpointRow>

        {chainId !== null && (
          <>
            <DetailLabel>Chain ID</DetailLabel>
            <DetailValue>{chainId}</DetailValue>
          </>
        )}
      </DetailsSection>

      {network.alias === 'base-sepolia' && <BaseSepoliaObolFaucet />}

      {totalUpstreams > 0 && (
        <UpstreamSection>
          <Accordion
            header={
              <UpstreamSectionLabel>
                {totalUpstreams}{' '}
                {totalUpstreams === 1 ? 'upstream' : 'upstreams'}
              </UpstreamSectionLabel>
            }
          >
            <UpstreamsGrid>
              {/* Local nodes first — they are the primary serving path. */}
              {localUpstreams.map(node => (
                <LocalNodeStatus key={node.appId} appData={node} />
              ))}
              {erpcUpstreams.map(upstream => (
                <RemoteNodeStatus key={upstream.id} upstream={upstream} />
              ))}
            </UpstreamsGrid>
          </Accordion>
        </UpstreamSection>
      )}
    </NetworkCardWrapper>
  );
}
