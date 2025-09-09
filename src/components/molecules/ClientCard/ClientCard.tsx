import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { StatusIndicator } from '../../atoms/StatusIndicator';
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
  display: 'flex',
  width: '218px',
  padding: '16px',
  flexDirection: 'column',
  alignItems: 'flex-start',
  gap: '8px',
  borderRadius: '8px',
  background: '#182D32', // var(--Backgrounds-03, #182D32)
});

const ClientHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  width: '100%',
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
  color: '#FFF',
  fontFamily: '"DM Sans"',
  fontSize: '16px',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: '24px',
});

const ClientVersion = styled(Text, {
  color: '#97B2B8',
  fontFamily: '"DM Sans"',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '500',
  lineHeight: '18px',
});

const ClientMetrics = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$2',
  width: '100%',
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
  const getStatusIndicatorStatus = () => {
    switch (status) {
      case 'synced':
        return 'synced';
      case 'syncing':
        return 'unsynced';
      case 'error':
        return 'unsynced';
      default:
        return 'synced';
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
            <ClientVersion>{data.version}</ClientVersion>
          </ClientDetails>
        </ClientInfo>
        <StatusIndicator status={getStatusIndicatorStatus()} size="sm" />
      </ClientHeader>
      <ClientMetrics>
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
