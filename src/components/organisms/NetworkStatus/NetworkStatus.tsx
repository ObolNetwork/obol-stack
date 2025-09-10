import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusIndicator } from '../../atoms/StatusIndicator';
import { Accordion } from '../../molecules/Accordion';
import { AnimatedNumber } from '../../molecules/AnimatedNumber';
import { ParticipationNumber } from '../../molecules/ParticipationNumber';
import { ClientCard, ClientConfig } from '../../molecules/ClientCard';
import { ChainData } from '../../../types/dashboard';

export interface NetworkStatusProps {
  chainData: ChainData;
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
  chainData,
  networkName,
  networkIcon,
  defaultExpanded = true,
}: NetworkStatusProps) {
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

          <ChainChip
            variant={chainData.network === 'mainnet' ? 'mainnet' : 'testnet'}
          />

          <ChainChip
            variant={
              chainData.nodeMode === 'fullMode' ? 'fullMode' : 'lightMode'
            }
          />
        </Flex>

        <StatusIndicator
          status={
            chainData.status === 'syncing'
              ? 'unsynced'
              : (chainData.status as 'synced' | 'unsynced')
          }
          size="md"
        />
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
                {chainData.blocks > 0 ? (
                  <AnimatedNumber
                    value={chainData.blocks}
                    animateLastDigit={true}
                    incrementInterval={12000}
                  />
                ) : (
                  'N/A'
                )}
              </MetricValue>
            </MetricItem>
            <MetricItem>
              <MetricLabel>FINALIZED EPOCH</MetricLabel>
              <MetricValue>
                {chainData.finalized > 0 ? (
                  <AnimatedNumber
                    value={chainData.finalized}
                    animateLastDigit={true}
                    incrementInterval={384000}
                  />
                ) : (
                  'N/A'
                )}
              </MetricValue>
            </MetricItem>
            <MetricItem>
              <MetricLabel>NODE MODE</MetricLabel>
              <MetricValue css={{ fontFamily: 'inherit' }}>
                {chainData.nodeMode === 'fullMode' ? 'Full Node' : 'Light Node'}
              </MetricValue>
            </MetricItem>
            <MetricItem>
              <MetricLabel>PARTICIPATION</MetricLabel>
              <MetricValue>
                {chainData.participation > 0 ? (
                  <ParticipationNumber
                    initialValue={chainData.participation}
                    target={chainData.participation}
                    standardDeviation={1}
                    updateInterval={4000}
                  />
                ) : (
                  'N/A'
                )}
              </MetricValue>
            </MetricItem>
          </MetricsGrid>

          {/* Embedded Client Components - only show for networks that have clients */}
          {chainData.clients && chainData.clients.length > 0 && (
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
                {chainData.clients.map(client => {
                  // Map role to proper display name
                  const roleDisplay =
                    client.role === 'execution'
                      ? 'Execution'
                      : client.role === 'consensus'
                        ? 'Consensus'
                        : 'Validator';

                  const config: ClientConfig = {
                    name: client.name,
                    role: roleDisplay,
                    icon: client.icon || '/default-client-icon.png',
                  };

                  return (
                    <ClientCard
                      key={`${client.name}-${client.version}`}
                      config={config}
                      data={{
                        version: client.version,
                        peers: client.peers,
                      }}
                      status={client.status}
                      peersTarget={client.peers}
                      peersStandardDeviation={2}
                      updateInterval={10000}
                    />
                  );
                })}
              </ClientsContainer>
            </ClientsSection>
          )}
        </NetworkContent>
      </Accordion>
    </Card>
  );
}
