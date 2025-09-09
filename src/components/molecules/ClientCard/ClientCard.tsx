import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Badge } from '../../atoms/Badge';
import { RandomWalkNumber } from '../RandomWalkNumber';

export interface ClientConfig {
  name: string;
  role: string;
  icon: string;
}

export interface ClientData {
  version: string;
  peers: number;
}

export interface ClientCardProps {
  config: ClientConfig;
  data: ClientData;
  status?: 'synced' | 'syncing' | 'error';
  peersTarget?: number;
  peersStandardDeviation?: number;
  updateInterval?: number;
}

const Card = styled(Box, {
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
  paddingTop: '$xxs',
});

const ClientMetrics = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$2',
});

const ClientMetricRow = styled(Flex, {
  justifyContent: 'space-between',
  fontSize: '$3',
  paddingTop: '$xxs',
});

const ClientMetricLabel = styled(Text, {
  color: '$muted',
});

const ClientMetricValue = styled(Text, {
  color: '$light',
  fontFamily: 'monospace',
});

export function ClientCard({
  config,
  data,
  status = 'synced',
  peersTarget = 72,
  peersStandardDeviation = 10,
  updateInterval = 4000,
}: ClientCardProps) {
  const getBadgeVariant = () => {
    switch (status) {
      case 'synced':
        return 'success';
      case 'syncing':
        return 'warning';
      case 'error':
        return 'error';
      default:
        return 'success';
    }
  };

  const getBadgeText = () => {
    switch (status) {
      case 'synced':
        return 'Synced';
      case 'syncing':
        return 'Syncing';
      case 'error':
        return 'Error';
      default:
        return 'Synced';
    }
  };

  return (
    <Card>
      <ClientHeader>
        <ClientInfo>
          <ClientAvatar
            css={{
              backgroundImage: `url(${config.icon})`,
              backgroundSize: 'cover',
              backgroundPosition: 'center',
              backgroundRepeat: 'no-repeat',
            }}
          />
          <ClientDetails>
            <ClientName>{config.name}</ClientName>
            <ClientRole>{config.role}</ClientRole>
          </ClientDetails>
        </ClientInfo>
        <Badge variant={getBadgeVariant()} size="sm">
          {getBadgeText()}
        </Badge>
      </ClientHeader>
      <ClientMetrics>
        <ClientMetricRow>
          <ClientMetricLabel>Version</ClientMetricLabel>
          <ClientMetricValue>{data.version}</ClientMetricValue>
        </ClientMetricRow>
        <ClientMetricRow>
          <ClientMetricLabel>Peers</ClientMetricLabel>
          <ClientMetricValue>
            <RandomWalkNumber
              initialValue={data.peers}
              target={peersTarget}
              standardDeviation={peersStandardDeviation}
              updateInterval={updateInterval}
              animateLastDigit={true}
            />
          </ClientMetricValue>
        </ClientMetricRow>
      </ClientMetrics>
    </Card>
  );
}
