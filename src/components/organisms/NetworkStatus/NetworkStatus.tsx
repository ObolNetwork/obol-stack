import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusPill } from '../../atoms/StatusIndicator';
import { ParticipationNumber } from '../../molecules/ParticipationNumber';
import { ClientCard } from '../../molecules/ClientCard';
import { AppData } from '../../../types/dashboard';
import { useWalletClient } from 'wagmi';
import { AddNetworkButton } from '../../molecules/AddNetworkButton';
import { AppIcon } from '../../atoms/AppIcon';

export interface NetworkStatusProps {
  appData: AppData;
}

const NetworkHeader = styled(Flex, {
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

const MetricsGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr',
  gap: '$lg',

  '@media (min-width: 480px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },

  '@bp1': {
    gridTemplateColumns: 'repeat(2, 1fr)',
    gridTemplateRows: 'repeat(2, 1fr)',
  },

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

export function NetworkStatus({ appData }: NetworkStatusProps) {
  const { data: walletClient } = useWalletClient();

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
      <NetworkHeader>
        <Flex
          css={{
            alignItems: 'center',
            gap: '$sm',
            flexWrap: 'wrap',
            width: 'auto',
          }}
        >
          <AppIcon app={appData.app} size={24} />

          <Flex css={{ flexDirection: 'column', gap: '$xxxs', width: 'auto' }}>
            <CardAppType>
              {appData.app === 'erpc'
                ? 'eRPC'
                : appData.app.charAt(0).toUpperCase() + appData.app.slice(1)}
            </CardAppType>
            <CardAppId>{appData.appId}</CardAppId>
          </Flex>

          <ChainChip variant={appData.chainName} />
          <ChainChip variant={appData.nodeMode} />
        </Flex>

        <Flex css={{ alignItems: 'center', gap: '$xxs', width: 'auto' }}>
          <AddNetworkButton
            chainName={appData.chainName}
            walletClient={walletClient}
          />
          <StatusPill status={appData.status} />
        </Flex>
      </NetworkHeader>

      {/* Metrics */}
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
        {appData.nodeMode === 'erpcProxy' && (
          <MetricItem>
            <MetricLabel>UPSTREAMS</MetricLabel>
            <MetricValue>{appData.upstreamCount ?? 0}</MetricValue>
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
                  : appData.nodeMode === 'erpcProxy'
                    ? 'RPC Proxy'
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

      {/* Clients - only for full nodes */}
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
                peers={appData.nodeMode === 'fullMode' ? client.peers : null}
                status={client.status}
                icon={client.icon}
              />
            ))}
          </ClientsContainer>
        </ClientsSection>
      )}
    </Card>
  );
}
