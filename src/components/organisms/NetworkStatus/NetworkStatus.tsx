import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Badge } from '../../atoms/Badge';
import { AnimatedNumber } from '../../molecules/AnimatedNumber';
import { ProgressSweep } from '../../molecules/ProgressSweep';
import { ParticipationNumber } from '../../molecules/ParticipationNumber';
import { ClientCard, ClientConfig } from '../../molecules/ClientCard';
import { DashboardData } from '../../../types/dashboard';

export interface NetworkStatusProps {
  data: DashboardData['ethereum'];
  clients: DashboardData['clients'];
}

const CardHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  marginBottom: '$lg',
});

const CardTitle = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
});

const MetricsGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr',
  gap: '$lg',

  '@bp1': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },
});

const MetricItem = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const MetricLabel = styled(Text, {
  fontSize: '$1',
  letterSpacing: '$wide',
  color: '$muted',
  textTransform: 'uppercase',
  marginBottom: '$1',
});

const MetricValue = styled(Text, {
  fontSize: '$8',
  fontWeight: '$semibold',
  color: '$light',
  fontFamily: 'monospace',

  '@bp1': {
    fontSize: '$9',
  },
});

const ClientsSection = styled(Box, {
  marginTop: '$lg',
  paddingTop: '$lg',
  borderTop: '1px solid rgba(255, 255, 255, 0.05)',
});

const ClientsGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr',
  gap: '$lg',

  '@bp1': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },
});

const ProgressContainer = styled(Box, {
  position: 'absolute',
  bottom: 0,
  left: 0,
  right: 0,
  borderBottomLeftRadius: '$4',
  borderBottomRightRadius: '$4',
  overflow: 'hidden',
});

export function NetworkStatus({ data, clients }: NetworkStatusProps) {
  // Client configurations
  const clientConfigs: Record<string, ClientConfig> = {
    nethermind: {
      name: 'Nethermind',
      role: 'Execution',
      icon: '/nethermind-icon.png',
    },
    nimbus: {
      name: 'Nimbus',
      role: 'Consensus',
      icon: '/nimbus-icon.png',
    },
  };

  return (
    <Card
      css={{
        padding: '$lg',
        '@md': { padding: '$xl' },
        position: 'relative',
        overflow: 'hidden',
      }}
    >
      <CardHeader>
        <CardTitle>Ethereum Mainnet</CardTitle>
        <Badge variant="success" size="sm">
          Synced
        </Badge>
      </CardHeader>

      {/* Metrics grid - Clean 2x2 layout */}
      <MetricsGrid>
        <MetricItem>
          <MetricLabel>BLOCK</MetricLabel>
          <MetricValue>
            <AnimatedNumber
              value={data.blocks}
              animateLastDigit={true}
              incrementInterval={12000}
            />
          </MetricValue>
        </MetricItem>
        <MetricItem>
          <MetricLabel>FINALIZED EPOCH</MetricLabel>
          <MetricValue>
            <AnimatedNumber
              value={Math.floor(data.blocks / 32) - 2}
              animateLastDigit={true}
              incrementInterval={384000}
            />
          </MetricValue>
        </MetricItem>
        <MetricItem>
          <MetricLabel>NODE MODE</MetricLabel>
          <MetricValue css={{ fontFamily: 'inherit' }}>Full</MetricValue>
        </MetricItem>
        <MetricItem>
          <MetricLabel>PARTICIPATION</MetricLabel>
          <MetricValue>
            <ParticipationNumber
              initialValue={data.participation}
              target={97.5}
              standardDeviation={1}
              updateInterval={4000}
            />
          </MetricValue>
        </MetricItem>
      </MetricsGrid>

      {/* Embedded Client Components */}
      <ClientsSection>
        <ClientsGrid>
          <ClientCard
            config={clientConfigs.nethermind}
            data={clients.nethermind}
            status="synced"
            peersTarget={72}
            peersStandardDeviation={10}
            updateInterval={4000}
          />
          <ClientCard
            config={clientConfigs.nimbus}
            data={clients.nimbus}
            status="synced"
            peersTarget={72}
            peersStandardDeviation={10}
            updateInterval={4000}
          />
        </ClientsGrid>
      </ClientsSection>

      {/* Progress sweep bar - positioned at bottom of card */}
      <ProgressContainer>
        <ProgressSweep duration={12000} className="rounded-none" />
      </ProgressContainer>
    </Card>
  );
}
