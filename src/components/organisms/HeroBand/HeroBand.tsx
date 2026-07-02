'use client';

import type { ReactNode } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { StatusPill } from '../../atoms/StatusIndicator';
import { useAgentInstances } from '../../../hooks/useAgents';
import { useServiceOffers } from '../../../hooks/useServiceOffers';
import type { DashboardData } from '../../../types/dashboard';
import { networkToChainName } from '../../../lib/utils/network-rpc';
import { formatUptime } from '@/lib/utils/format-uptime';

interface HeroBandProps {
  data: DashboardData | null;
}

function displayInstanceName(instance: {
  runtime: string;
  id: string;
}): string {
  if (instance.runtime === 'hermes' && instance.id === 'obol-agent')
    return 'Stack agent';
  if (instance.runtime === 'openclaw' && instance.id === 'default')
    return 'OpenClaw';
  return instance.id;
}

const HeroCard = styled(Box, {
  position: 'relative',
  overflow: 'hidden',
  backgroundColor: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$4',
  padding: '$xl $xl $lg',
});

const GradientWash = styled(Box, {
  position: 'absolute',
  inset: 0,
  background:
    'linear-gradient(108deg, rgba(47,228,171,0.07), rgba(182,234,92,0.03) 42%, transparent 70%)',
  pointerEvents: 'none',
});

const Eyebrow = styled(Text, {
  fontSize: '$1',
  color: '$obolGreen',
  fontWeight: '$medium',
  letterSpacing: '0.03em',
  textTransform: 'uppercase',
  marginBottom: 10,
});

const Title = styled(Text, {
  fontSize: '$9',
  fontWeight: '$bold',
  color: '$light',
  letterSpacing: '-0.02em',
  lineHeight: 1,
  margin: 0,
});

const Description = styled(Text, {
  margin: 0,
  fontSize: '$3',
  color: '$body',
  lineHeight: 1.5,
});

const MetricContainer = styled(Box, {
  flex: 1,
  minWidth: 0,
  padding: '0 $lg',
});

const MetricLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontWeight: '$medium',
  letterSpacing: '0.03em',
  textTransform: 'uppercase',
  marginBottom: 10,
});

const MetricValue = styled(Text, {
  fontSize: '$9',
  fontWeight: '$semibold',
  letterSpacing: '-0.02em',
  lineHeight: 1,
  color: '$light',
  fontFamily: 'monospace',
});

const MetricSub = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  marginTop: 8,
});

function HeroMetric({
  label,
  value,
  sub,
  last,
  tooltip,
}: {
  label: string;
  value: string;
  sub: ReactNode;
  last?: boolean;
  tooltip?: string;
}) {
  return (
    <MetricContainer
      title={tooltip}
      css={{
        borderRight: last ? 'none' : '1px solid $stroke',
        cursor: tooltip ? 'help' : undefined,
      }}
    >
      <MetricLabel>{label}</MetricLabel>
      <MetricValue>{value}</MetricValue>
      <MetricSub>{sub}</MetricSub>
    </MetricContainer>
  );
}

export function HeroBand({ data }: HeroBandProps) {
  const { instances } = useAgentInstances();
  const { offers } = useServiceOffers();

  const agentCount = instances.length;
  const networkCount = data?.erpcData?.networks?.length ?? 0;
  const serviceCount = offers.length;
  const uptimeHours = data?.nodeOverview?.uptime?.hours ?? 0;

  // Same local-vs-remote split NetworksSection derives: a network is "local"
  // when a locally-synced node serves its chain; otherwise it is remote-only.
  const localChainNames = new Set(
    (data?.localNodes ?? []).map(node => node.chainName)
  );
  const localNetworkCount =
    data?.erpcData?.networks?.filter(n => {
      const chainName = networkToChainName(n.alias);
      return chainName !== null && localChainNames.has(chainName);
    }).length ?? 0;
  const remoteNetworkCount = networkCount - localNetworkCount;

  const isHealthy = data !== null;
  const uptimeStr = formatUptime(uptimeHours);

  const agentNames = instances
    .map(i => displayInstanceName(i))
    .join(' \u00b7 ');
  const networkSub: ReactNode =
    networkCount === 0 ? (
      'not configured'
    ) : (
      <>
        {localNetworkCount > 0 && (
          <Text as="span" css={{ fontSize: '$1', color: '$light' }}>
            {localNetworkCount} local
          </Text>
        )}
        {localNetworkCount > 0 && remoteNetworkCount > 0 && ' \u00b7 '}
        {remoteNetworkCount > 0 && (
          <Text as="span" css={{ fontSize: '$1', color: '$muted' }}>
            {remoteNetworkCount} remote
          </Text>
        )}
      </>
    );
  const servicesSub = serviceCount > 0 ? `${serviceCount} on sale` : 'none yet';

  const description =
    agentCount > 0 && networkCount > 0
      ? `${agentCount} agent${agentCount !== 1 ? 's' : ''} running across ${networkCount} network${networkCount !== 1 ? 's' : ''}, transacting over x402. Cluster up ${uptimeStr}.`
      : agentCount > 0
        ? `${agentCount} agent${agentCount !== 1 ? 's' : ''} running, transacting over x402. Cluster up ${uptimeStr}.`
        : `Cluster up ${uptimeStr}. Add agents with obol agent init.`;

  return (
    <HeroCard>
      <GradientWash />
      <Box css={{ position: 'relative' }}>
        <Eyebrow>Obol Stack</Eyebrow>
        <Flex css={{ alignItems: 'center', gap: '$xs', marginBottom: '$xxs' }}>
          <Title as="h1">Local cluster</Title>
          <StatusPill
            status={isHealthy ? 'synced' : 'syncing'}
            label={isHealthy ? 'Healthy' : 'Starting'}
          />
        </Flex>
        <Description>{description}</Description>

        <Flex css={{ marginTop: '$xl', maxWidth: 620 }}>
          <HeroMetric
            label="Agents"
            value={String(agentCount)}
            sub={agentNames || 'none running'}
          />
          <HeroMetric
            label="Networks"
            value={String(networkCount)}
            sub={networkSub}
            tooltip="Served by the eRPC gateway: local nodes first, remote upstreams as fallback. Configure by running: obol stack edit, or add a network via the stack config."
          />
          <HeroMetric
            label="Services on Sale"
            value={String(serviceCount)}
            sub={servicesSub}
            last
          />
        </Flex>
      </Box>
    </HeroCard>
  );
}
