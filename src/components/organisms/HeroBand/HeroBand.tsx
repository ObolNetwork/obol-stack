'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { StatusPill } from '../../atoms/StatusIndicator';
import { useAgentInstances } from '../../../hooks/useAgents';
import { useServiceOffers } from '../../../hooks/useServiceOffers';
import type { DashboardData } from '../../../types/dashboard';

interface HeroBandProps {
  data: DashboardData | null;
}

function formatUptime(hours: number): string {
  const days = Math.floor(hours / 24);
  const remainingHours = hours % 24;
  if (days > 0) return `${days}d ${remainingHours}h`;
  if (remainingHours > 0) return `${remainingHours}h`;
  return '0h';
}

function displayInstanceName(instance: {
  runtime: string;
  id: string;
}): string {
  if (instance.runtime === 'hermes' && instance.id === 'obol-agent')
    return 'Obol Agent';
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
  fontSize: 32,
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
  fontSize: 32,
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
}: {
  label: string;
  value: string;
  sub: string;
  last?: boolean;
}) {
  return (
    <MetricContainer
      css={{
        borderRight: last ? 'none' : '1px solid $stroke',
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

  const syncedNetworks =
    data?.erpcData?.networks?.filter(n => n.state === 'OK').length ?? 0;
  const syncingNetworks = networkCount - syncedNetworks;

  const isHealthy = data !== null;
  const uptimeStr = formatUptime(uptimeHours);

  const agentNames = instances
    .map(i => displayInstanceName(i))
    .join(' \u00b7 ');
  const networkSub =
    syncingNetworks > 0
      ? `${syncedNetworks} synced \u00b7 ${syncingNetworks} syncing`
      : networkCount > 0
        ? `${syncedNetworks} synced`
        : 'none connected';
  const servicesSub =
    serviceCount > 0 ? `${serviceCount} listed` : 'none listed';

  const description =
    agentCount > 0
      ? `${agentCount} agent${agentCount !== 1 ? 's' : ''} running across ${networkCount} network${networkCount !== 1 ? 's' : ''}, transacting over x402. Cluster up ${uptimeStr}.`
      : networkCount > 0
        ? `${networkCount} network${networkCount !== 1 ? 's' : ''} connected. Cluster up ${uptimeStr}.`
        : `Starting up. Cluster up ${uptimeStr}.`;

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
          />
          <HeroMetric
            label="No. of Services"
            value={String(serviceCount)}
            sub={servicesSub}
            last
          />
        </Flex>
      </Box>
    </HeroCard>
  );
}
