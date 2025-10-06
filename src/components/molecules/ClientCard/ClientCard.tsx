import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { StatusIndicator } from '../../atoms/StatusIndicator';
import { Icon } from '../../atoms/Icon';

export interface ClientCardProps {
  name: string;
  version: string;
  peers: number | null;
  status?: 'synced' | 'syncing' | 'error';
  icon?: string;
}

const Card = styled(Box, {
  display: 'flex',
  width: '218px',
  padding: '$sm',
  flexDirection: 'column',
  alignItems: 'flex-start',
  gap: '$xxs',
  borderRadius: '$3',
  background: '$bg03',
});

const ClientHeader = styled(Flex, {
  alignItems: 'flex-start',
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
  backgroundColor: '$secondaryBgDefault',
});

const ClientDetails = styled(Box, {});

const ClientName = styled(Text, {
  color: '$white',
  fontFamily: '$fonts$body',
  fontSize: '$4',
  fontStyle: 'normal',
  fontWeight: '$semibold',
  lineHeight: '$base',
});

const ClientVersion = styled(Text, {
  color: '$body',
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$medium',
  lineHeight: '$short',
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

const ClientMetricValueContainer = styled(Flex, {
  flex: 0,
  alignItems: 'center',
  gap: '$xxxs',
});

const ClientMetricValue = styled(Text, {
  color: '$light',
  fontFamily: 'monospace',
});

export function ClientCard({
  name,
  version,
  peers,
  status = 'synced',
  icon,
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
          {icon && (
            <ClientAvatar
              css={{
                backgroundImage: `url(${icon})`,
                backgroundSize: 'contain',
                backgroundPosition: 'center',
                backgroundRepeat: 'no-repeat',
              }}
            />
          )}
          <ClientDetails>
            <ClientName>{name}</ClientName>
            <ClientVersion>{version}</ClientVersion>
          </ClientDetails>
        </ClientInfo>
        <StatusIndicator status={getStatusIndicatorStatus()} size="sm" />
      </ClientHeader>
      <ClientMetrics>
        {peers !== null && (
          <ClientMetricRow>
            <ClientMetricLabel>Peers</ClientMetricLabel>
            <ClientMetricValueContainer>
              <Icon name="peers" size="xxs" />
              <ClientMetricValue>{peers}</ClientMetricValue>
            </ClientMetricValueContainer>
          </ClientMetricRow>
        )}
      </ClientMetrics>
    </Card>
  );
}
