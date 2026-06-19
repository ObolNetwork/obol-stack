'use client';

import { useState } from 'react';
import {
  Box,
  Flex,
  Text,
  Button,
  Link,
  styled,
  OpenInNew,
} from '@obolnetwork/obol-ui';
import { StatusPill } from '../atoms/StatusIndicator';
import { Avatar } from '../atoms/Avatar';
import {
  useAgentStatus,
  useTunnelURL,
  useAgentWalletMetadata,
} from '../../hooks/useAgents';
import { useWalletAssetBalances } from '../../hooks/useWalletAssetBalances';
import type { AgentInstance } from '@/types/agent';

function displayInstanceName(instance: AgentInstance): string {
  if (instance.runtime === 'hermes' && instance.id === 'obol-agent')
    return 'Obol Agent';
  if (instance.runtime === 'openclaw' && instance.id === 'default')
    return 'OpenClaw';
  return instance.id;
}

function runtimeLabel(instance: AgentInstance): string {
  return instance.runtime === 'hermes' ? 'Hermes' : 'OpenClaw';
}

function formatUptime(hours: number): string {
  const days = Math.floor(hours / 24);
  const remainingHours = hours % 24;
  if (days > 0) return `${days}d ${remainingHours}h`;
  if (remainingHours > 0) return `${remainingHours}h`;
  return '0h';
}

function withCurrentIngressPort(rawUrl: string): string {
  if (typeof window === 'undefined') return rawUrl;
  try {
    const target = new URL(rawUrl);
    const current = new URL(window.location.href);
    if (
      target.hostname.endsWith('.obol.stack') &&
      current.hostname.endsWith('obol.stack')
    ) {
      target.protocol = current.protocol;
      if (!target.port && current.port) target.port = current.port;
    }
    return target.toString().replace(/\/$/, '');
  } catch {
    return rawUrl;
  }
}

// Styled components
const CardContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
  padding: '$md',
  borderRadius: '$2',
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  transition: 'border-color 200ms ease',
  '&:hover': { borderColor: '$body' },
});

const RuntimeBadge = styled(Text, {
  fontSize: 10,
  fontWeight: '$semibold',
  color: '$body',
  backgroundColor: 'rgba(255,255,255,0.05)',
  border: '1px solid rgba(255,255,255,0.12)',
  borderRadius: '$1',
  padding: '2px 6px',
  lineHeight: 1.2,
  textTransform: 'uppercase',
});

const StatStrip = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr 1fr 1fr',
  backgroundColor: '$bg01',
  border: '1px solid $stroke',
  borderRadius: '$2',
  padding: '$xs 0',
});

const StatCell = styled(Box, {
  padding: '0 $md',
});

const StatLabel = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  marginBottom: '$xxxs',
});

const StatValue = styled(Text, {
  fontSize: '$3',
  color: '$light',
  fontWeight: '$semibold',
  fontFamily: 'monospace',
});

const StatSub = styled(Text, {
  fontSize: 10,
  color: '$muted',
  marginTop: 2,
});

const ModelLabel = styled(Text, {
  fontSize: '$1',
  color: '$muted',
});

const ModelValue = styled(Text, {
  fontSize: '$2',
  color: '$light',
  fontFamily: 'monospace',
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

interface HomeAgentCardProps {
  instance: AgentInstance;
  uptimeHours: number;
  onChat?: (instance: AgentInstance) => void;
}

export function HomeAgentCard({
  instance,
  uptimeHours,
  onChat,
}: HomeAgentCardProps) {
  const { status, loading } = useAgentStatus(instance);
  const { metadata } = useAgentWalletMetadata(instance);
  const { tunnelURL } = useTunnelURL();

  const primaryAddress = metadata?.addresses?.[0]?.address ?? null;
  const { balances } = useWalletAssetBalances(primaryAddress);

  const name = displayInstanceName(instance);
  const initial = name[0]?.toUpperCase() ?? '#';

  const displayStatus = loading
    ? 'pending'
    : status?.podStatus?.toLowerCase() === 'running'
      ? 'running'
      : status?.podStatus?.toLowerCase() === 'pending'
        ? 'pending'
        : status
          ? 'error'
          : 'unknown';

  const statusPillStatus =
    displayStatus === 'running'
      ? 'synced'
      : displayStatus === 'pending'
        ? 'syncing'
        : displayStatus === 'error'
          ? 'error'
          : 'unsynced';

  const statusLabel = loading ? 'Loading...' : status?.podStatus || 'Unknown';

  // Pick the first balance for display
  const primaryBalance =
    balances.length > 0
      ? `${balances[0].formatted} ${balances[0].token.symbol}`
      : '\u2014';
  const balanceSub = balances.length > 0 ? balances[0].chain.shortName : '';

  const modelDisplay = status?.model
    ? status.model.replace(/^openai\//, '')
    : '\u2014';

  const dashboardBaseUrl =
    instance.runtime === 'openclaw'
      ? tunnelURL || `http://${instance.hostname}`
      : instance.dashboardHostname
        ? `http://${instance.dashboardHostname}`
        : null;
  const dashboardUrl = dashboardBaseUrl
    ? withCurrentIngressPort(dashboardBaseUrl)
    : null;

  return (
    <CardContainer>
      {/* Top row */}
      <Flex css={{ alignItems: 'flex-start', gap: '$xs' }}>
        <Avatar alt={name} fallback={initial} />
        <Flex
          css={{ flex: 1, minWidth: 0, flexDirection: 'column', gap: '$xxxs' }}
        >
          <Flex css={{ alignItems: 'center', gap: '$xxs' }}>
            <Text
              css={{ fontSize: '$5', fontWeight: '$semibold', color: '$light' }}
            >
              {name}
            </Text>
            <RuntimeBadge>{runtimeLabel(instance)}</RuntimeBadge>
          </Flex>
          <Text
            css={{
              fontSize: '$1',
              color: '$body',
              fontFamily: 'monospace',
              whiteSpace: 'nowrap',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
            }}
          >
            {instance.hostname}
          </Text>
        </Flex>
        <StatusPill status={statusPillStatus} label={statusLabel} />
      </Flex>

      {/* Stat strip */}
      <StatStrip>
        <StatCell css={{ borderRight: '1px solid $stroke' }}>
          <StatLabel>Balance</StatLabel>
          <StatValue>{primaryBalance}</StatValue>
          <StatSub>{balanceSub}</StatSub>
        </StatCell>
        <StatCell css={{ borderRight: '1px solid $stroke' }}>
          <StatLabel>Requests &middot; 24h</StatLabel>
          <StatValue>{'\u2014'}</StatValue>
          <StatSub>LiteLLM</StatSub>
        </StatCell>
        <StatCell>
          <StatLabel>Uptime</StatLabel>
          <StatValue>{formatUptime(uptimeHours)}</StatValue>
          <StatSub>continuous</StatSub>
        </StatCell>
      </StatStrip>

      {/* Primary model */}
      <Flex css={{ flexDirection: 'column', gap: '$xxxs' }}>
        <ModelLabel>Primary model</ModelLabel>
        <ModelValue>{modelDisplay}</ModelValue>
      </Flex>

      {/* Actions */}
      <Flex css={{ alignItems: 'center', gap: '$xs' }}>
        <Button
          color="primary"
          disabled={displayStatus !== 'running'}
          onClick={() => onChat?.(instance)}
          css={{ fontSize: '$2', height: 'auto', padding: '6px 12px' }}
        >
          Chat
        </Button>
        {dashboardUrl && (
          <Link
            href={dashboardUrl}
            target="_blank"
            rel="noopener noreferrer"
            css={{
              fontSize: '$2',
              display: 'inline-flex',
              alignItems: 'center',
              gap: '$xxxs',
            }}
          >
            Dashboard <OpenInNew size="sm" />
          </Link>
        )}
        <Box css={{ flex: 1 }} />
      </Flex>
    </CardContainer>
  );
}
