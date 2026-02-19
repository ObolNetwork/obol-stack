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
import { StatusPill } from '../../atoms/StatusIndicator';
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
      : status?.podStatus?.toLowerCase() === 'pending'
        ? 'pending'
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
        <StatusPill
          status={
            displayStatus === 'running'
              ? 'synced'
              : displayStatus === 'pending'
                ? 'syncing'
                : displayStatus === 'error'
                  ? 'error'
                  : 'unsynced'
          }
          label={loading ? 'Loading...' : status?.podStatus || 'Unknown'}
        />
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
