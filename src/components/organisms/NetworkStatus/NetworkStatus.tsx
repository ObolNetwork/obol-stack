import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusIndicator } from '../../atoms/StatusIndicator';
import { Accordion } from '../../molecules/Accordion';
import { ParticipationNumber } from '../../molecules/ParticipationNumber';
import { ClientCard } from '../../molecules/ClientCard';
import { AppData } from '../../../types/dashboard';
import { useWalletClient } from 'wagmi';
import { AddNetworkButton } from '../../molecules/AddNetworkButton';
import { AppIcon } from '../../atoms/AppIcon';

export interface NetworkStatusProps {
  appData: AppData;
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
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$semibold',
  lineHeight: '$tall2',
  textTransform: 'uppercase',
  marginBottom: '$1',
});

const MetricValue = styled(Text, {
  color: '$white',
  fontFamily: '$fonts$body',
  fontSize: '$6',
  fontStyle: 'normal',
  fontWeight: '$semibold',
  lineHeight: '$base',
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
  appData,
  defaultExpanded = true,
}: NetworkStatusProps) {
  const { data: walletClient } = useWalletClient();

  const headerContent = (
    <NetworkHeader
      css={{
        justifyContent: 'space-between',
        paddingRight: '$xl',
      }}
    >
      <Flex css={{ alignItems: 'center', gap: '$xxs' }}>
        {/* App Icon */}
        {<AppIcon app={appData.app} size={24} />}

        <CardTitle>
          {appData.app.charAt(0).toUpperCase() + appData.app.slice(1)}
        </CardTitle>

        <ChainChip variant={appData.chainName} />

        <ChainChip variant={appData.nodeMode} />
      </Flex>

      <Flex css={{ alignItems: 'center', gap: '$xxs', width: 'auto' }}>
        <AddNetworkButton
          chainName={appData.chainName}
          walletClient={walletClient}
        />

        <StatusIndicator
          status={
            appData.status === 'syncing'
              ? 'unsynced'
              : (appData.status as 'synced' | 'unsynced')
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
      <Accordion header={headerContent} defaultExpanded={defaultExpanded}>
        <NetworkContent>
          {/* Metrics grid - Clean 2x2 layout */}
          <MetricsGrid>
            <MetricItem>
              <MetricLabel>BLOCK HEIGHT</MetricLabel>
              <MetricValue>{appData.blocks.toLocaleString()}</MetricValue>
            </MetricItem>
            {appData.nodeMode === 'fullMode' && (
              <MetricItem>
                <MetricLabel>FINALIZED EPOCH</MetricLabel>
                <MetricValue>{appData.finalized.toLocaleString()}</MetricValue>
              </MetricItem>
            )}
            <MetricItem>
              <MetricLabel>NODE MODE</MetricLabel>
              <MetricValue css={{ fontFamily: 'inherit' }}>
                {appData.nodeMode === 'fullMode'
                  ? 'Full Node'
                  : appData.nodeMode === 'lightMode'
                    ? 'Light Node'
                    : appData.nodeMode === 'aztecSequencer'
                      ? 'Sequencer'
                      : 'Unknown'}
              </MetricValue>
            </MetricItem>
            {appData.nodeMode === 'fullMode' && (
              <MetricItem>
                <MetricLabel>PARTICIPATION</MetricLabel>
                <MetricValue>
                  <ParticipationNumber value={appData.participation} />
                </MetricValue>
              </MetricItem>
            )}
          </MetricsGrid>

          {/* Embedded Client Components - only show for networks that have clients */}
          {appData.clients && appData.clients.length > 0 && (
            <ClientsSection>
              <Text
                css={{
                  color: '$body',
                  fontFamily: '$fonts$body',
                  fontSize: '$2',
                  fontStyle: 'normal',
                  fontWeight: '$semibold',
                  lineHeight: '$taller',
                  textTransform: 'uppercase',
                  marginBottom: '$md',
                }}
              >
                CLIENTS
              </Text>
              <ClientsContainer>
                {appData.clients.map(client => (
                  <ClientCard
                    key={client.name}
                    name={client.name}
                    version={client.version}
                    peers={
                      appData.nodeMode === 'fullMode' ? client.peers : null
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
