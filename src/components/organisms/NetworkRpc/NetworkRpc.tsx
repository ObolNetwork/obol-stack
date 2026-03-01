'use client';

import React, { MouseEvent, useState, useRef, useEffect } from 'react';
import { WalletClient } from 'viem';
import { Box, Flex, Text, styled, theme } from '@obolnetwork/obol-ui';
import { useWalletClient } from 'wagmi';
import { Up } from '@/icons';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusPill } from '../../atoms/StatusIndicator';
import { AppIcon } from '../../atoms/AppIcon';
import { CopyButton } from '../../atoms/CopyButton';
import { AddNetworkButton } from '../../molecules/AddNetworkButton';
import {
  AppData,
  ErpcNetwork,
  ErpcUpstream,
  ChainName,
  ClientStatusType,
} from '../../../types/dashboard';
import {
  sortNetworks,
  networkToChainName,
  networkToStatus,
  chainIdFromEvmId,
  isMainnetNetwork,
  erpcUrl,
  groupUpstreamsByNetwork,
  formatMs,
  formatRate,
} from '../../../lib/utils/network-rpc';

export interface NetworkRpcProps {
  appData: AppData;
}

// ── Header ────────────────────────────────────────────────────────────────────

const RpcHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$sm',
  marginBottom: '$md',
});

const CardTitle = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
  lineHeight: '$base',
});

// ── Summary bar ─────────────────────────────────────────────────────────────

const SummaryBar = styled(Flex, {
  gap: '$md',
  marginBottom: '$sm',
  flexWrap: 'wrap',
});

const SummaryItem = styled(Flex, {
  alignItems: 'center',
  gap: '$xxs',
});

const SummaryLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  letterSpacing: '$wider',
});

const SummaryValue = styled(Text, {
  fontSize: '$2',
  color: '$light',
  fontWeight: '$semibold',
});

// ── Network list ──────────────────────────────────────────────────────────────

const NetworkList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const NetworkCardWrapper = styled(Box, {
  borderRadius: '$3',
  background: '$bg04',
  border: '1px solid $stroke',
  overflow: 'hidden',
});

const NetworkCardHeader = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
  padding: '$xs $sm',
});

const ChipSlot = styled(Box, {
  minWidth: 76,
  flexShrink: 0,
});

const TestnetBadge = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '$wider',
  flexShrink: 0,
});

const HeaderSpacer = styled(Box, { flex: 1 });

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

// $body = #9CC2C9 — clearly readable on dark bg, properly secondary
const DetailLabel = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '$wider',
  whiteSpace: 'nowrap',
});

// $light = #D9EEF3 — bright and clearly legible
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

// ── Expand/collapse ─────────────────────────────────────────────────────────

const ExpandChevron = styled(Box, {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: 24,
  height: 24,
  cursor: 'pointer',
  flexShrink: 0,
  transition: 'transform 0.3s ease',
  transform: 'rotate(180deg)',
  variants: {
    expanded: {
      true: { transform: 'rotate(0deg)' },
    },
  },
});

const ExpandableContent = styled(Box, {
  overflow: 'hidden',
  transition: 'all 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
  variants: {
    expanded: {
      true: { opacity: 1 },
      false: { opacity: 0 },
    },
  },
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

const HealthBadge = styled(Flex, {
  alignItems: 'center',
  gap: '4px',
  borderRadius: '$pill',
  paddingLeft: '$xs',
  paddingRight: '$xs',
  paddingTop: '2px',
  paddingBottom: '2px',
  fontSize: '$1',
  fontWeight: '$medium',
  flexShrink: 0,
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
        <HealthBadge
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
        </HealthBadge>
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
    </UpstreamCardBox>
  );
}

// ── NetworkRow ────────────────────────────────────────────────────────────────

function NetworkRow({
  network,
  walletClient,
  upstreams,
}: {
  network: ErpcNetwork;
  walletClient: WalletClient | null | undefined;
  upstreams?: ErpcUpstream[];
}) {
  const [isExpanded, setIsExpanded] = useState(false);
  const contentRef = useRef<HTMLDivElement>(null);
  const hasUpstreams = upstreams && upstreams.length > 0;

  const chainName = networkToChainName(network.alias);
  const status = networkToStatus(network.state);
  const chainId = chainIdFromEvmId(network.id);
  const isMainnet = isMainnetNetwork(network.id);
  const isTestnet = !isMainnet;
  const endpoint = `${erpcUrl()}/${network.alias}`;

  // Height animation (same pattern as Accordion)
  useEffect(() => {
    if (!contentRef.current) return;
    if (isExpanded) {
      const scrollHeight = contentRef.current.scrollHeight;
      contentRef.current.style.height = '0px';
      requestAnimationFrame(() => {
        if (contentRef.current) {
          contentRef.current.style.height = `${scrollHeight}px`;
        }
      });
    } else {
      if (contentRef.current.style.height === 'auto') {
        const scrollHeight = contentRef.current.scrollHeight;
        contentRef.current.style.height = `${scrollHeight}px`;
        requestAnimationFrame(() => {
          if (contentRef.current) {
            contentRef.current.style.height = '0px';
          }
        });
      } else {
        contentRef.current.style.height = '0px';
      }
    }
  }, [isExpanded]);

  const handleTransitionEnd = () => {
    if (isExpanded && contentRef.current) {
      contentRef.current.style.height = 'auto';
    }
  };

  const handleToggle = () => {
    if (hasUpstreams) setIsExpanded(prev => !prev);
  };

  return (
    <NetworkCardWrapper>
      <NetworkCardHeader
        onClick={handleToggle}
        css={hasUpstreams ? { cursor: 'pointer' } : undefined}
      >
        <ChipSlot>
          <ChainChip
            variant={chainName ?? (isMainnet ? 'mainnet' : 'sepolia')}
            label={
              chainName
                ? undefined
                : network.alias
                    .replace(/-/g, ' ')
                    .replace(/\b\w/g, c => c.toUpperCase())
            }
          />
        </ChipSlot>

        {isTestnet && <TestnetBadge>testnet</TestnetBadge>}

        <HeaderSpacer />

        <StatusPill status={status} />

        <Box onClick={(e: MouseEvent) => e.stopPropagation()}>
          <AddNetworkButton
            chainName={chainName ?? (isMainnet ? 'mainnet' : 'sepolia')}
            walletClient={chainName ? walletClient : null}
          />
        </Box>

        {hasUpstreams && (
          <ExpandChevron expanded={isExpanded}>
            <Up size="s" color={theme.colors.body.value} />
          </ExpandChevron>
        )}
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
        <ExpandableContent
          ref={contentRef}
          expanded={isExpanded}
          onTransitionEnd={handleTransitionEnd}
        >
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
        </ExpandableContent>
      )}
    </NetworkCardWrapper>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export function NetworkRpc({ appData }: NetworkRpcProps) {
  const { data: walletClient } = useWalletClient();
  const erpcStatus = appData.erpcStatus;
  const networks = sortNetworks(erpcStatus?.networks ?? []);
  const upstreamsByNetwork = groupUpstreamsByNetwork(
    erpcStatus?.upstreams ?? []
  );

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
        <Flex css={{ alignItems: 'center', gap: '$xs' }}>
          <AppIcon app="ethereum" size={20} />
          <CardTitle>Ethereum</CardTitle>
        </Flex>
        <StatusPill status={appData.status} />
      </RpcHeader>

      {(erpcStatus?.upstreamCount ?? 0) > 0 && (
        <SummaryBar>
          <SummaryItem>
            <SummaryLabel>Upstreams</SummaryLabel>
            <SummaryValue>{erpcStatus?.upstreamCount}</SummaryValue>
          </SummaryItem>
        </SummaryBar>
      )}

      <NetworkList>
        {networks.map(network => (
          <NetworkRow
            key={network.id}
            network={network}
            walletClient={walletClient}
            upstreams={upstreamsByNetwork.get(network.id)}
          />
        ))}
      </NetworkList>
    </Card>
  );
}
