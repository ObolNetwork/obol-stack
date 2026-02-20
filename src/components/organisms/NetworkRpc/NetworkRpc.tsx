'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { StatusPill } from '../../atoms/StatusIndicator';
import { AppIcon } from '../../atoms/AppIcon';
import {
  AppData,
  ErpcUpstream,
  CHAIN_ID_TO_NAME,
  ChainIdType,
} from '../../../types/dashboard';

export interface NetworkRpcProps {
  appData: AppData;
}

// ── Layout ──────────────────────────────────────────────────────────────────

const RpcHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$md',
  marginBottom: '$2xl',
  flexWrap: 'wrap',
});

const CardAppType = styled(Text, {
  fontSize: '$2',
  fontWeight: '$medium',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
});

const CardAppId = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
  fontFamily: 'monospace',
  lineHeight: '$tight',
});

const SummaryGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'repeat(2, 1fr)',
  gap: '$lg',
  marginBottom: '$2xl',

  '@media (min-width: 480px)': {
    gridTemplateColumns: 'repeat(4, 1fr)',
  },
});

const SummaryItem = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const SummaryLabel = styled(Text, {
  color: '$body',
  fontSize: '$2',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  lineHeight: '$tall2',
});

const SummaryValue = styled(Text, {
  color: '$white',
  fontSize: '$6',
  fontWeight: '$semibold',
  lineHeight: '$base',
});

// ── Network group ────────────────────────────────────────────────────────────

const NetworkGroup = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
  '& + &': { marginTop: '$md' },
});

const NetworkGroupHeader = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
  marginBottom: '$xs',
});

const NetworkLabel = styled(Text, {
  fontSize: '$2',
  fontWeight: '$semibold',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
});

const UpstreamCount = styled(Text, {
  fontSize: '$1',
  fontWeight: '$medium',
  color: '$muted',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  paddingLeft: '$xs',
  paddingRight: '$xs',
  paddingTop: '2px',
  paddingBottom: '2px',
});

const UpstreamsGrid = styled(Box, {
  display: 'grid',
  gap: '$sm',
  gridTemplateColumns: '1fr',

  '@media (min-width: 640px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },

  '@media (min-width: 1024px)': {
    gridTemplateColumns: 'repeat(3, 1fr)',
  },
});

// ── Upstream card ────────────────────────────────────────────────────────────

const UpstreamCard = styled(Box, {
  padding: '$sm',
  borderRadius: '$3',
  background: '$bg04',
  border: '1px solid $stroke',
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const UpstreamCardHeader = styled(Flex, {
  justifyContent: 'space-between',
  alignItems: 'flex-start',
  width: '100%',
});

const UpstreamName = styled(Text, {
  color: '$white',
  fontSize: '$4',
  fontWeight: '$semibold',
  lineHeight: '$base',
  fontFamily: 'monospace',
});

const CordonedBadge = styled(Flex, {
  alignItems: 'center',
  gap: '4px',
  borderRadius: '$pill',
  paddingLeft: '$xs',
  paddingRight: '$xs',
  paddingTop: '2px',
  paddingBottom: '2px',
  fontSize: '$1',
  fontWeight: '$medium',
});

const MetricRow = styled(Flex, {
  justifyContent: 'space-between',
  alignItems: 'center',
  fontSize: '$3',
  paddingTop: '$xxxs',
});

const MetricLabel = styled(Text, {
  color: '$muted',
});

const MetricValue = styled(Text, {
  color: '$light',
  fontFamily: 'monospace',
});

// ── Helpers ──────────────────────────────────────────────────────────────────

function parseErpcNetwork(network: string): {
  chainId: number | null;
  label: string;
} {
  // network is "evm:1", "evm:11155111", etc.
  const match = network.match(/^evm:(\d+)$/);
  if (!match) return { chainId: null, label: network };
  const chainId = parseInt(match[1], 10);
  const name = CHAIN_ID_TO_NAME[chainId as ChainIdType];
  const label = name
    ? name.charAt(0).toUpperCase() + name.slice(1)
    : `Chain ${chainId}`;
  return { chainId, label };
}

function formatMs(seconds: number): string {
  return `${Math.round(seconds * 1000)}ms`;
}

function formatRate(rate: number): string {
  if (rate === 0) return '0%';
  return `${(rate * 100).toFixed(2)}%`;
}

// ── Sub-components ────────────────────────────────────────────────────────────

function UpstreamDetail({ upstream }: { upstream: ErpcUpstream }) {
  const { metrics } = upstream;
  const isHealthy = !metrics.cordoned && metrics.errorRate === 0;

  return (
    <UpstreamCard>
      <UpstreamCardHeader>
        <UpstreamName>{upstream.id}</UpstreamName>
        <CordonedBadge
          css={
            metrics.cordoned
              ? { background: '#FF6B6B18', color: '#FF6B6B' }
              : isHealthy
                ? { background: '#2FE4AB18', color: '#2FE4AB' }
                : { background: '#F5A62318', color: '#F5A623' }
          }
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
        </CordonedBadge>
      </UpstreamCardHeader>

      <Box css={{ borderTop: '1px solid $stroke', paddingTop: '$xs' }}>
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
            css={metrics.errorRate > 0 ? { color: '#FF6B6B' } : undefined}
          >
            {formatRate(metrics.errorRate)}
          </MetricValue>
        </MetricRow>
        {metrics.blockHeadLag > 0 && (
          <MetricRow>
            <MetricLabel>Block lag</MetricLabel>
            <MetricValue css={{ color: '#F5A623' }}>
              {metrics.blockHeadLag}
            </MetricValue>
          </MetricRow>
        )}
        {metrics.throttledRate > 0 && (
          <MetricRow>
            <MetricLabel>Throttled</MetricLabel>
            <MetricValue css={{ color: '#F5A623' }}>
              {formatRate(metrics.throttledRate)}
            </MetricValue>
          </MetricRow>
        )}
        {metrics.cordoned && metrics.lastCordonedReason && (
          <MetricRow>
            <MetricLabel>Reason</MetricLabel>
            <MetricValue css={{ color: '#FF6B6B', fontFamily: 'inherit' }}>
              {metrics.lastCordonedReason}
            </MetricValue>
          </MetricRow>
        )}
      </Box>
    </UpstreamCard>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export function NetworkRpc({ appData }: NetworkRpcProps) {
  const { erpcStatus } = appData;

  // Group upstreams by network string
  const networkGroups = new Map<string, ErpcUpstream[]>();
  for (const upstream of erpcStatus?.upstreams ?? []) {
    const existing = networkGroups.get(upstream.network) ?? [];
    networkGroups.set(upstream.network, [...existing, upstream]);
  }

  return (
    <Card
      css={{
        position: 'relative',
        overflow: 'hidden',
        backgroundColor: '$bg03',
        borderRadius: '$2',
        padding: '$sm $md',
      }}
    >
      <RpcHeader>
        <Flex
          css={{
            alignItems: 'center',
            gap: '$sm',
            flexWrap: 'wrap',
            width: 'auto',
          }}
        >
          <AppIcon app="erpc" size={24} />
          <Flex css={{ flexDirection: 'column', gap: '$xxxs', width: 'auto' }}>
            <CardAppType>Network RPC</CardAppType>
            <CardAppId>{appData.appId}</CardAppId>
          </Flex>
        </Flex>
        <StatusPill status={appData.status} />
      </RpcHeader>

      <SummaryGrid>
        <SummaryItem>
          <SummaryLabel>Networks</SummaryLabel>
          <SummaryValue>{erpcStatus?.networkCount ?? 0}</SummaryValue>
        </SummaryItem>
        <SummaryItem>
          <SummaryLabel>Upstreams</SummaryLabel>
          <SummaryValue>{erpcStatus?.upstreamCount ?? 0}</SummaryValue>
        </SummaryItem>
        <SummaryItem>
          <SummaryLabel>Status</SummaryLabel>
          <SummaryValue
            css={{
              fontSize: '$4',
              color: erpcStatus?.status === 'OK' ? '#2FE4AB' : '#FF6B6B',
            }}
          >
            {erpcStatus?.message ?? '—'}
          </SummaryValue>
        </SummaryItem>
      </SummaryGrid>

      {networkGroups.size > 0 && (
        <Box>
          {Array.from(networkGroups.entries()).map(([network, upstreams]) => {
            const { label } = parseErpcNetwork(network);
            return (
              <NetworkGroup key={network}>
                <NetworkGroupHeader>
                  <NetworkLabel>{label}</NetworkLabel>
                  <UpstreamCount>
                    {upstreams.length}{' '}
                    {upstreams.length === 1 ? 'upstream' : 'upstreams'}
                  </UpstreamCount>
                </NetworkGroupHeader>
                <UpstreamsGrid>
                  {upstreams.map(upstream => (
                    <UpstreamDetail key={upstream.id} upstream={upstream} />
                  ))}
                </UpstreamsGrid>
              </NetworkGroup>
            );
          })}
        </Box>
      )}
    </Card>
  );
}
