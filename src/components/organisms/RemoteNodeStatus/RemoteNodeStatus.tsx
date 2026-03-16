import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { ErpcUpstream } from '../../../types/dashboard';
import { Badge } from '../../atoms/Badge';
import { CardAppId } from '../../atoms/AppIcon';
import { ChainChip } from '../../atoms/ChainChip';
import { formatMs, formatRate } from '../../../lib/utils/network-rpc';

export interface RemoteNodeStatusProps {
  upstream: ErpcUpstream;
}

const CardBox = styled(Box, {
  borderRadius: '$3',
  background: '$bg04',
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

export function RemoteNodeStatus({ upstream }: RemoteNodeStatusProps) {
  const { metrics } = upstream;
  const isHealthy = !metrics.cordoned && metrics.errorRate === 0;

  const healthVariant = metrics.cordoned
    ? 'red'
    : isHealthy
      ? 'green'
      : 'yellow';
  const healthLabel = metrics.cordoned
    ? 'Cordoned'
    : isHealthy
      ? 'Healthy'
      : 'Degraded';

  return (
    <CardBox>
      <CardHeader>
        <Flex
          css={{ flexDirection: 'column', gap: '$xxxs', minWidth: 0, flex: 1 }}
        >
          <CardAppId css={{ fontSize: '$4' }}>{upstream.id}</CardAppId>
          <Box css={{ alignSelf: 'flex-start' }}>
            <ChainChip variant="remoteNode" small />
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
        <MetricRow>
          <MetricLabel>p50 / p90</MetricLabel>
          <MetricValue
            css={{ fontFamily: 'monospace', fontWeight: '$regular' }}
          >
            {formatMs(metrics.responseQuantiles.p50)} /{' '}
            {formatMs(metrics.responseQuantiles.p90)}
          </MetricValue>
        </MetricRow>

        <MetricRow>
          <MetricLabel>Requests</MetricLabel>
          <MetricValue>{metrics.requestsTotal.toLocaleString()}</MetricValue>
        </MetricRow>

        <MetricRow>
          <MetricLabel>Error rate</MetricLabel>
          <MetricValue
            css={metrics.errorRate > 0 ? { color: '$test' } : undefined}
          >
            {formatRate(metrics.errorRate)}
          </MetricValue>
        </MetricRow>

        {metrics.blockHeadLag > 0 && (
          <MetricRow>
            <MetricLabel>Block lag</MetricLabel>
            <MetricValue css={{ color: '$obolGold' }}>
              {metrics.blockHeadLag}
            </MetricValue>
          </MetricRow>
        )}

        {metrics.throttledRate > 0 && (
          <MetricRow>
            <MetricLabel>Throttled</MetricLabel>
            <MetricValue css={{ color: '$obolGold' }}>
              {formatRate(metrics.throttledRate)}
            </MetricValue>
          </MetricRow>
        )}

        {metrics.cordoned && metrics.lastCordonedReason && (
          <MetricRow>
            <MetricLabel>Reason</MetricLabel>
            <MetricValue css={{ color: '$test', fontFamily: 'inherit' }}>
              {metrics.lastCordonedReason}
            </MetricValue>
          </MetricRow>
        )}
      </MetricsSection>
    </CardBox>
  );
}
