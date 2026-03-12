'use client';

import { WalletClient } from 'viem';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { AppIcon, CardAppType, CardAppId } from '../../atoms/AppIcon';
import { Badge } from '../../atoms/Badge';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusPill } from '../../atoms/StatusIndicator';
import { CopyButton } from '../../atoms/CopyButton';
import { AddNetworkButton } from '../../molecules/AddNetworkButton';
import { ErpcNetwork, ErpcUpstream } from '../../../types/dashboard';
import {
  networkToChainName,
  networkToStatus,
  chainIdFromEvmId,
  isMainnetNetwork,
  erpcUrl,
  formatMs,
  formatRate,
} from '../../../lib/utils/network-rpc';

// ── Network card ──────────────────────────────────────────────────────────────

const NetworkCardWrapper = styled(Box, {
  borderRadius: '$3',
  background: '$bg04',
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

// ── Upstream cards ───────────────────────────────────────────────────────────

const UpstreamSection = styled(Box, {
  borderTop: '1px solid $stroke',
  padding: '$sm',
});

const UpstreamSectionLabel = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '$wider',
  marginBottom: '$xs',
});

const UpstreamsGrid = styled(Box, {
  display: 'grid',
  gap: '$xs',
  gridTemplateColumns: '1fr',
  '@media (min-width: 640px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },
});

const UpstreamCardBox = styled(Box, {
  padding: '$xs $sm',
  borderRadius: '$3',
  background: '$bg05',
  border: '1px solid $stroke',
  display: 'flex',
  flexDirection: 'column',
  gap: '$xxs',
});

const UpstreamCardHeader = styled(Flex, {
  justifyContent: 'space-between',
  alignItems: 'center',
  width: '100%',
});

const UpstreamName = styled(Text, {
  color: '$light',
  fontSize: '$2',
  fontWeight: '$semibold',
  fontFamily: 'monospace',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
});

const MetricRow = styled(Flex, {
  justifyContent: 'space-between',
  alignItems: 'center',
  fontSize: '$1',
});

const MetricLabel = styled(Text, { color: '$body', fontSize: '$1' });
const MetricValue = styled(Text, {
  color: '$light',
  fontFamily: 'monospace',
  fontSize: '$1',
});

// ── UpstreamDetail ──────────────────────────────────────────────────────────

function UpstreamDetail({ upstream }: { upstream: ErpcUpstream }) {
  const { metrics } = upstream;
  const isHealthy = !metrics.cordoned && metrics.errorRate === 0;

  return (
    <UpstreamCardBox>
      <UpstreamCardHeader>
        <UpstreamName>{upstream.id}</UpstreamName>
        <Badge
          variant={metrics.cordoned ? 'red' : isHealthy ? 'green' : 'yellow'}
        >
          <Box
            css={{
              width: 6,
              height: 6,
              borderRadius: '$round',
              backgroundColor: 'currentColor',
              flexShrink: 0,
            }}
          />
          {metrics.cordoned ? 'Cordoned' : isHealthy ? 'Healthy' : 'Degraded'}
        </Badge>
      </UpstreamCardHeader>

      <Box css={{ borderTop: '1px solid $stroke', paddingTop: '$xxs' }}>
        <MetricRow>
          <MetricLabel>p50 / p90</MetricLabel>
          <MetricValue>
            {formatMs(metrics.responseQuantiles.p50)} /{' '}
            {formatMs(metrics.responseQuantiles.p90)}
          </MetricValue>
        </MetricRow>
        <MetricRow>
          <MetricLabel>Requests</MetricLabel>
          <MetricValue>{metrics.requestsTotal.toLocaleString()}</MetricValue>
        </MetricRow>
        <MetricRow>
          <MetricLabel>Error rate</MetricLabel>
          <MetricValue
            css={metrics.errorRate > 0 ? { color: '$test' } : undefined}
          >
            {formatRate(metrics.errorRate)}
          </MetricValue>
        </MetricRow>
        {metrics.blockHeadLag > 0 && (
          <MetricRow>
            <MetricLabel>Block lag</MetricLabel>
            <MetricValue css={{ color: '$obolGold' }}>
              {metrics.blockHeadLag}
            </MetricValue>
          </MetricRow>
        )}
        {metrics.throttledRate > 0 && (
          <MetricRow>
            <MetricLabel>Throttled</MetricLabel>
            <MetricValue css={{ color: '$obolGold' }}>
              {formatRate(metrics.throttledRate)}
            </MetricValue>
          </MetricRow>
        )}
        {metrics.cordoned && metrics.lastCordonedReason && (
          <MetricRow>
            <MetricLabel>Reason</MetricLabel>
            <MetricValue css={{ color: '$test', fontFamily: 'inherit' }}>
              {metrics.lastCordonedReason}
            </MetricValue>
          </MetricRow>
        )}
      </Box>
    </UpstreamCardBox>
  );
}

// ── ErpcStatus ────────────────────────────────────────────────────────────────

export function ErpcStatus({
  network,
  walletClient,
  upstreams,
}: {
  network: ErpcNetwork;
  walletClient: WalletClient | null | undefined;
  upstreams?: ErpcUpstream[];
}) {
  const chainName = networkToChainName(network.alias);
  const status = networkToStatus(network.state);
  const chainId = chainIdFromEvmId(network.id);
  const isMainnet = isMainnetNetwork(network.id);
  const endpoint = `${erpcUrl()}/${network.alias}`;
  const hasUpstreams = upstreams && upstreams.length > 0;

  return (
    <NetworkCardWrapper>
      <NetworkCardHeader>
        <Flex css={{ alignItems: 'center', gap: '$sm' }}>
          <AppIcon app="erpc" size={24} />
          <Flex css={{ flexDirection: 'column', gap: '$xxxs', width: 'auto' }}>
            <CardAppType>Remote Node erpc</CardAppType>
            <CardAppId>{network.alias}</CardAppId>
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

      {hasUpstreams && (
        <UpstreamSection>
          <UpstreamSectionLabel>
            {upstreams.length}{' '}
            {upstreams.length === 1 ? 'upstream' : 'upstreams'}
          </UpstreamSectionLabel>
          <UpstreamsGrid>
            {upstreams.map(upstream => (
              <UpstreamDetail key={upstream.id} upstream={upstream} />
            ))}
          </UpstreamsGrid>
        </UpstreamSection>
      )}
    </NetworkCardWrapper>
  );
}
