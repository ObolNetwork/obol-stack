import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Badge } from '../../atoms/Badge';
import { AnimatedNumber } from '../../molecules/AnimatedNumber';
import { ProgressSweep } from '../../molecules/ProgressSweep';
import { ParticipationNumber } from '../../molecules/ParticipationNumber';
import { RandomWalkNumber } from '../../molecules/RandomWalkNumber';
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

const ClientCard = styled(Box, {
  backgroundColor: 'rgba(255, 255, 255, 0.05)',
  borderRadius: '$3',
  padding: '$md',
});

const ClientHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  marginBottom: '$md',
});

const ClientInfo = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
});

const ClientAvatar = styled(Box, {
  width: '$xl',
  height: '$xl',
  borderRadius: '$3',
  backgroundColor: 'rgba(255, 255, 255, 0.1)',
});

const ClientDetails = styled(Box, {});

const ClientName = styled(Text, {
  fontWeight: '$semibold',
  color: '$light',
});

const ClientRole = styled(Text, {
  fontSize: '$3',
  color: '$muted',
});

const ClientMetrics = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$2',
});

const ClientMetricRow = styled(Flex, {
  justifyContent: 'space-between',
  fontSize: '$3',
});

const ClientMetricLabel = styled(Text, {
  color: '$muted',
});

const ClientMetricValue = styled(Text, {
  color: '$light',
  fontFamily: 'monospace',
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
  const clientConfig = {
    nethermind: {
      name: 'Nethermind',
      role: 'Execution',
      icon: '/nethermind-icon.png',
      avatarBg: 'bg-blue-600',
    },
    nimbus: {
      name: 'Nimbus',
      role: 'Consensus',
      icon: '/nimbus-icon.png',
      avatarBg: 'bg-orange-600',
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
          {/* Nethermind Client */}
          <ClientCard>
            <ClientHeader>
              <ClientInfo>
                <ClientAvatar
                  css={{
                    backgroundImage: `url(${clientConfig.nethermind.icon})`,
                    backgroundSize: 'cover',
                    backgroundPosition: 'center',
                    backgroundRepeat: 'no-repeat',
                  }}
                />
                <ClientDetails>
                  <ClientName>{clientConfig.nethermind.name}</ClientName>
                  <ClientRole>{clientConfig.nethermind.role}</ClientRole>
                </ClientDetails>
              </ClientInfo>
              <Badge variant="success" size="sm">
                Synced
              </Badge>
            </ClientHeader>
            <ClientMetrics>
              <ClientMetricRow>
                <ClientMetricLabel>Version</ClientMetricLabel>
                <ClientMetricValue>
                  {clients.nethermind.version}
                </ClientMetricValue>
              </ClientMetricRow>
              <ClientMetricRow>
                <ClientMetricLabel>Peers</ClientMetricLabel>
                <ClientMetricValue>
                  <RandomWalkNumber
                    initialValue={clients.nethermind.peers}
                    target={72}
                    standardDeviation={10}
                    updateInterval={4000}
                    animateLastDigit={true}
                  />
                </ClientMetricValue>
              </ClientMetricRow>
            </ClientMetrics>
          </ClientCard>

          {/* Nimbus Client */}
          <ClientCard>
            <ClientHeader>
              <ClientInfo>
                <ClientAvatar
                  css={{
                    backgroundImage: `url(${clientConfig.nimbus.icon})`,
                    backgroundSize: 'cover',
                    backgroundPosition: 'center',
                    backgroundRepeat: 'no-repeat',
                  }}
                />
                <ClientDetails>
                  <ClientName>{clientConfig.nimbus.name}</ClientName>
                  <ClientRole>{clientConfig.nimbus.role}</ClientRole>
                </ClientDetails>
              </ClientInfo>
              <Badge variant="success" size="sm">
                Synced
              </Badge>
            </ClientHeader>
            <ClientMetrics>
              <ClientMetricRow>
                <ClientMetricLabel>Version</ClientMetricLabel>
                <ClientMetricValue>{clients.nimbus.version}</ClientMetricValue>
              </ClientMetricRow>
              <ClientMetricRow>
                <ClientMetricLabel>Peers</ClientMetricLabel>
                <ClientMetricValue>
                  <RandomWalkNumber
                    initialValue={clients.nimbus.peers}
                    target={72}
                    standardDeviation={10}
                    updateInterval={4000}
                    animateLastDigit={true}
                  />
                </ClientMetricValue>
              </ClientMetricRow>
            </ClientMetrics>
          </ClientCard>
        </ClientsGrid>
      </ClientsSection>

      {/* Progress sweep bar - positioned at bottom of card */}
      <ProgressContainer>
        <ProgressSweep duration={12000} className="rounded-none" />
      </ProgressContainer>
    </Card>
  );
}
