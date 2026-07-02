import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { LocalNodeData, NODE_MODES } from '../../../types/dashboard';
import { Badge } from '../../atoms/Badge';
import { CardAppId } from '../../atoms/AppIcon';
import { ChainChip } from '../../atoms/ChainChip';
import { ParticipationNumber } from '../../molecules/ParticipationNumber';

export interface LocalNodeStatusProps {
  appData: LocalNodeData;
}

const CardBox = styled(Box, {
  borderRadius: '$2',
  background: '$bg03',
  border: '1px solid $stroke',
  overflow: 'hidden',
});

const CardHeader = styled(Flex, {
  alignItems: 'flex-start',
  justifyContent: 'space-between',
  gap: '$sm',
  padding: '$xs $sm $xxs',
});

const MetricsSection = styled(Box, {
  borderTop: '1px solid $stroke',
  padding: '$xxs $sm $sm',
});

const MetricRow = styled(Flex, {
  justifyContent: 'space-between',
  alignItems: 'baseline',
  width: '100%',
  paddingTop: '$xs',
});

const MetricLabel = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '$wider',
  flexShrink: 0,
  paddingRight: '$sm',
});

const MetricValue = styled(Text, {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '$white',
  textAlign: 'right',
});

export function LocalNodeStatus({ appData }: LocalNodeStatusProps) {
  const healthVariant =
    appData.status === 'synced'
      ? 'green'
      : appData.status === 'syncing'
        ? 'yellow'
        : 'red';
  const healthLabel =
    appData.status === 'synced'
      ? 'Healthy'
      : appData.status === 'syncing'
        ? 'Syncing'
        : 'Error';

  return (
    <CardBox>
      <CardHeader>
        <Flex
          css={{ flexDirection: 'column', gap: '$xxxs', minWidth: 0, flex: 1 }}
        >
          <CardAppId css={{ fontSize: '$4' }}>{appData.appId}</CardAppId>
          <Box css={{ alignSelf: 'flex-start' }}>
            <ChainChip variant={appData.nodeMode} small />
          </Box>
        </Flex>
        <Badge
          variant={healthVariant}
          css={{ flexShrink: 0, alignSelf: 'flex-start' }}
        >
          <Box
            css={{
              width: 6,
              height: 6,
              borderRadius: '$round',
              backgroundColor: 'currentColor',
              flexShrink: 0,
            }}
          />
          {healthLabel}
        </Badge>
      </CardHeader>

      <MetricsSection>
        {appData.blocks !== undefined && (
          <MetricRow>
            <MetricLabel>Block height</MetricLabel>
            <MetricValue>{appData.blocks.toLocaleString()}</MetricValue>
          </MetricRow>
        )}

        {appData.nodeMode === NODE_MODES.FULL_MODE &&
          appData.finalized !== undefined && (
            <MetricRow>
              <MetricLabel>Finalized</MetricLabel>
              <MetricValue>{appData.finalized.toLocaleString()}</MetricValue>
            </MetricRow>
          )}

        {appData.nodeMode === NODE_MODES.FULL_MODE &&
          appData.participation !== undefined && (
            <MetricRow>
              <MetricLabel>Participation</MetricLabel>
              <MetricValue>
                <ParticipationNumber value={appData.participation} />
              </MetricValue>
            </MetricRow>
          )}

        {appData.clients &&
          appData.clients.length > 0 &&
          appData.clients.map(client => (
            <MetricRow key={client.name}>
              <MetricLabel>
                {client.name.charAt(0).toUpperCase() + client.name.slice(1)}
              </MetricLabel>
              <Flex css={{ alignItems: 'center', gap: '$xxs' }}>
                <MetricValue
                  css={{ fontFamily: 'monospace', fontWeight: '$regular' }}
                >
                  v{client.version}
                </MetricValue>
                {client.peers > 0 && (
                  <Text
                    css={{
                      color: '$body',
                      fontSize: '$2',
                      fontFamily: 'monospace',
                    }}
                  >
                    · {client.peers}p
                  </Text>
                )}
              </Flex>
            </MetricRow>
          ))}
      </MetricsSection>
    </CardBox>
  );
}
