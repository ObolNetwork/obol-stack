import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusIndicator } from '../../atoms/StatusIndicator';
import { Accordion } from '../../molecules/Accordion';
import { ParticipationNumber } from '../../molecules/ParticipationNumber';
import { ClientCard } from '../../molecules/ClientCard';
import { ChainData } from '../../../types/dashboard';
import { ChainIcon } from '../../atoms/ChainIcon';

export interface NetworkStatusProps {
  chainData: ChainData;
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
  marginRight: '$xxxs',
  marginLeft: '$xxxs',
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
  fontFamily: '$fonts$body',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: '28px',
  textTransform: 'uppercase',
  marginBottom: '$1',
});

const MetricValue = styled(Text, {
  color: '#FFF',
  fontFamily: '$fonts$body',
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
  gap: '$xs',
});

export function NetworkStatus({
  chainData,
  defaultExpanded = true,
}: NetworkStatusProps) {
  const headerContent = (
    <NetworkHeader
      css={{
        justifyContent: 'space-between',
        paddingRight: '$xl',
      }}
    >
      <Flex css={{ alignItems: 'center', gap: '$xxs' }}>
        {/* Network Icon */}
        {<ChainIcon chain={chainData.chainName} size={24} />}

        <CardTitle>
          {chainData.chainName.charAt(0).toUpperCase() +
            chainData.chainName.slice(1)}
        </CardTitle>

        <ChainChip
          variant={chainData.network === 'mainnet' ? 'mainnet' : 'testnet'}
        />

        <ChainChip
          variant={chainData.nodeMode === 'fullMode' ? 'fullMode' : 'lightMode'}
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
      <Accordion header={headerContent} defaultExpanded={defaultExpanded}>
        <NetworkContent>
          {/* Metrics grid - Clean 2x2 layout */}
          <MetricsGrid>
            <MetricItem>
              <MetricLabel>BLOCK HEIGHT</MetricLabel>
              <MetricValue>{chainData.blocks.toLocaleString()}</MetricValue>
            </MetricItem>
            {chainData.nodeMode === 'fullMode' && (
              <MetricItem>
                <MetricLabel>FINALIZED EPOCH</MetricLabel>
                <MetricValue>
                  {chainData.finalized.toLocaleString()}
                </MetricValue>
              </MetricItem>
            )}
            <MetricItem>
              <MetricLabel>NODE MODE</MetricLabel>
              <MetricValue css={{ fontFamily: 'inherit' }}>
                {chainData.nodeMode === 'fullMode'
                  ? 'Full Node'
                  : chainData.nodeMode === 'lightMode'
                    ? 'Light Node'
                    : chainData.nodeMode === 'aztec-fullnode'
                      ? 'Full Node (Archiver)'
                      : chainData.nodeMode === 'aztec-sequencer'
                        ? 'Sequencer'
                        : chainData.nodeMode === 'aztec-prover'
                          ? 'Prover'
                          : 'Unknown'}
              </MetricValue>
            </MetricItem>
            {chainData.nodeMode === 'fullMode' && (
              <MetricItem>
                <MetricLabel>PARTICIPATION</MetricLabel>
                <MetricValue>
                  <ParticipationNumber value={chainData.participation} />
                </MetricValue>
              </MetricItem>
            )}
          </MetricsGrid>

          {/* Embedded Client Components - only show for networks that have clients */}
          {chainData.clients && chainData.clients.length > 0 && (
            <ClientsSection>
              <Text
                css={{
                  color: '$body',
                  fontFamily: '$fonts$body',
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
                {chainData.clients.map(client => (
                  <ClientCard
                    key={client.name}
                    name={client.name}
                    version={client.version}
                    peers={
                      chainData.nodeMode === 'fullMode' ? client.peers : null
                    }
                    status={client.status}
                    icon={client.icon}
                  />
                ))}
              </ClientsContainer>
            </ClientsSection>
          )}
        </NetworkContent>
      </Accordion>
    </Card>
  );
}
