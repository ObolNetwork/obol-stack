import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusPill } from '../../atoms/StatusIndicator';
import { ParticipationNumber } from '../../molecules/ParticipationNumber';
import { ClientCard } from '../../molecules/ClientCard';
import { AppData, NODE_MODES } from '../../../types/dashboard';
import { useWalletClient } from 'wagmi';
import { AddNetworkButton } from '../../molecules/AddNetworkButton';
import { AppIcon, CardAppType, CardAppId } from '../../atoms/AppIcon';

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
  const title =
    appData.nodeMode === NODE_MODES.LIGHT_MODE
      ? 'Local Light Client'
      : appData.nodeMode === NODE_MODES.FULL_MODE
        ? 'Local Full Node'
        : appData.nodeMode === NODE_MODES.AZTEC_SEQUENCER
          ? 'Aztec Sequencer'
          : '';
  return (
    <Card
      css={{
        position: 'relative',
        overflow: 'hidden',
        backgroundColor: '$bg04',
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
              {title} {appData.app}
            </CardAppType>
            <CardAppId>{appData.appId}</CardAppId>
          </Flex>

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
          <MetricValue>{appData.blocks?.toLocaleString() ?? '—'}</MetricValue>
        </MetricItem>
        {appData.nodeMode === 'fullMode' && (
          <MetricItem>
            <MetricLabel>FINALIZED EPOCH</MetricLabel>
            <MetricValue>
              {appData.finalized?.toLocaleString() ?? '—'}
            </MetricValue>
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
        {appData.nodeMode === 'fullMode' && appData.participation && (
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
