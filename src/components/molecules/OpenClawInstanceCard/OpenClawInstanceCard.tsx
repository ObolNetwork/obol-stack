'use client';

import {
  Box,
  Flex,
  Text,
  Button,
  Link,
  styled,
  OpenInNew,
} from '@obolnetwork/obol-ui';
import { StatusIndicator } from '../../atoms/StatusIndicator';
import {
  useOpenClawStatus,
  useOpenClawToken,
} from '../../../hooks/useOpenClaw';
import type { OpenClawInstance } from '@/types/openclaw';

interface OpenClawInstanceCardProps {
  instance: OpenClawInstance;
  onSelect?: (instance: OpenClawInstance) => void;
}

const CardContainer = styled(Box, {
  padding: '$sm $md',
  borderRadius: '$2',
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  transition: 'border-color 0.2s ease',

  '&:hover': {
    borderColor: '$body',
  },
});

const TopRow = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  marginBottom: '$lg',
});

const InstanceName = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
});

const StatusBadge = styled(Flex, {
  alignItems: 'center',
  width: 'fit-content',
  gap: '4px',
  borderRadius: '$pill',
  paddingTop: '$xxxs',
  paddingBottom: '$xxxs',
  paddingLeft: '$xs',
  paddingRight: '$xs',
  fontSize: '$1',
  fontWeight: '$medium',

  variants: {
    variant: {
      running: {
        backgroundColor: '#2FE4AB15',
        color: '#2FE4AB',
      },
      pending: {
        backgroundColor: '#F5A62315',
        color: '#F5A623',
      },
      error: {
        backgroundColor: '#FF6B6B15',
        color: '#FF6B6B',
      },
      unknown: {
        backgroundColor: '#6b7a7d15',
        color: '#6b7a7d',
      },
    },
  },
});

const MetaItem = styled(Flex, {
  alignItems: 'center',
  gap: '$xxxs',
  marginBottom: '$xxs',
  marginTop: '$xs',
});

const MetaLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
});

const MetaValue = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
});

const ActionsRow = styled(Flex, {
  paddingTop: '$lg',
  alignItems: 'center',
  gap: '$xs',
});

export function OpenClawInstanceCard({
  instance,
  onSelect,
}: OpenClawInstanceCardProps) {
  const { status, loading } = useOpenClawStatus(instance.id);

  const displayStatus = loading
    ? 'pending'
    : status?.podStatus?.toLowerCase() === 'running'
      ? 'running'
      : status
        ? 'error'
        : 'unknown';

  const { token } = useOpenClawToken(instance.id);

  const dashboardUrl = `http://${instance.namespace}.obol.stack${token ? `/#token=${token}` : ''}`;

  return (
    <CardContainer>
      <TopRow>
        <Flex css={{ alignItems: 'center', gap: '$xs' }}>
          <InstanceName>
            {instance.id === 'default' ? 'OpenClaw' : instance.id}
          </InstanceName>
        </Flex>
        <StatusBadge
          variant={displayStatus as 'running' | 'pending' | 'error' | 'unknown'}
        >
          <StatusIndicator
            status={displayStatus === 'running' ? 'synced' : 'unsynced'}
            size="sm"
          />
          {loading ? 'Loading...' : status?.podStatus || 'Unknown'}
        </StatusBadge>
      </TopRow>

      <MetaItem>
        <MetaLabel>Host:</MetaLabel>
        <MetaValue>{instance.hostname}</MetaValue>
      </MetaItem>
      {!loading && status?.model && (
        <MetaItem>
          <MetaLabel>Model:</MetaLabel>
          <MetaValue>{status.model}</MetaValue>
        </MetaItem>
      )}

      <ActionsRow>
        <Button
          color="primary"
          onClick={() => onSelect?.(instance)}
          css={{ fontSize: '$3', height: '$sm' }}
        >
          Chat
        </Button>
        <Link
          href={dashboardUrl}
          target="_blank"
          css={{
            fontSize: '13px',
          }}
        >
          Dashboard
          <OpenInNew size="sm" />
        </Link>
      </ActionsRow>
    </CardContainer>
  );
}
