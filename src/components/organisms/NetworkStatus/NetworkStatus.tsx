import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusIndicator } from '../../atoms/StatusIndicator';
import { Accordion } from '../../molecules/Accordion';
import { AnimatedNumber } from '../../molecules/AnimatedNumber';
import { ParticipationNumber } from '../../molecules/ParticipationNumber';
import { ClientCard, ClientConfig } from '../../molecules/ClientCard';
import { DashboardData } from '../../../types/dashboard';

export interface NetworkStatusProps {
  data: DashboardData['ethereum'] | DashboardData['aztec'];
  clients?: DashboardData['clients'];
  networkName: string;
  networkIcon: React.ReactNode;
  defaultExpanded?: boolean;
}

const NetworkHeader = styled(Flex, {
  alignItems: 'center',
  gap: '$md',
});

const CardTitle = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
});

const NetworkContent = styled(Box, {
  paddingTop: '$2xl',
});

const MetricsGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr',
  gap: '$lg',

  // Small devices: single column
  '@media (min-width: 480px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },

  // Medium devices: 2 rows with 2 items each
  '@bp1': {
    gridTemplateColumns: 'repeat(2, 1fr)',
    gridTemplateRows: 'repeat(2, 1fr)',
  },

  // Large devices: single row with all 4 items
  '@media (min-width: 1024px)': {
    gridTemplateColumns: 'repeat(4, 1fr)',
    gridTemplateRows: '1fr',
  },
});

const MetricItem = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const MetricLabel = styled(Text, {
  color: '$body',
  fontFamily: '"DM Sans"',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: '28px',
  textTransform: 'uppercase',
  marginBottom: '$1',
});

const MetricValue = styled(Text, {
  color: '#FFF',
  fontFamily: '"DM Sans"',
  fontSize: '20px',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: '24px',
});

const ClientsSection = styled(Box, {
  marginTop: '$lg',
  paddingTop: '$lg',
});

const ClientsContainer = styled(Box, {
  display: 'flex',
  flexWrap: 'wrap',
  gap: '$lg',
});

export function NetworkStatus({
  data,
  clients,
  networkName,
  networkIcon,
  defaultExpanded = true,
}: NetworkStatusProps) {
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

  const headerContent = (
    <NetworkHeader>
      <Flex
        css={{
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: '$md',
          flex: 1,
          paddingRight: '$sm',
        }}
      >
        <Flex css={{ alignItems: 'center', gap: '$md' }}>
          {/* Network Icon */}
          {networkIcon}

          <CardTitle>{networkName}</CardTitle>

          <ChainChip variant="mainnet" />

          <ChainChip variant="fullMode" />
        </Flex>

        <StatusIndicator status="synced" size="md" />
      </Flex>
    </NetworkHeader>
  );

  return (
    <Card
      css={{
        position: 'relative',
        overflow: 'hidden',
        padding: '0',
      }}
    >
      <Accordion
        header={headerContent}
        defaultExpanded={defaultExpanded}
        onToggle={isExpanded => {
          // You can add any additional logic here when accordion toggles
          console.log('Accordion toggled:', isExpanded);
        }}
      >
        <NetworkContent>
          {/* Metrics grid - Clean 2x2 layout */}
          <MetricsGrid>
            <MetricItem>
              <MetricLabel>BLOCK HEIGHT</MetricLabel>
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
              <MetricValue css={{ fontFamily: 'inherit' }}>
                Full Node
              </MetricValue>
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

          {/* Embedded Client Components - only show for networks that have clients */}
          {clients && (
            <ClientsSection>
              <Text
                css={{
                  color: '$body',
                  fontFamily: '"DM Sans"',
                  fontSize: '12px',
                  fontStyle: 'normal',
                  fontWeight: '600',
                  lineHeight: '28px',
                  textTransform: 'uppercase',
                  marginBottom: '$md',
                }}
              >
                CLIENTS
              </Text>
              <ClientsContainer>
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
              </ClientsContainer>
            </ClientsSection>
          )}
        </NetworkContent>
      </Accordion>
    </Card>
  );
}
