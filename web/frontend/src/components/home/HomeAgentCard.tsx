'use client';

import { Box, Flex, Text, Link, styled, OpenInNew } from '@obolnetwork/obol-ui';
import { Button } from '@/components/atoms/Button';
import { tones } from '../../lib/ui/tones';
import { StatusPill } from '../atoms/StatusIndicator';
import { Avatar } from '../atoms/Avatar';
import { CopyButton } from '../atoms/CopyButton';
import { WalletBalanceList } from '../molecules/WalletBalanceList';
import {
  useAgentStatus,
  useAgentWalletMetadata,
} from '../../hooks/useAgents';
import { useWalletAssetBalances } from '../../hooks/useWalletAssetBalances';
import { sortBalances } from '@/lib/wallet/sort-balances';
import type { AgentInstance } from '@/types/agent';
import { truncateAddress } from '@/utils/address';
import { formatUptime } from '@/lib/utils/format-uptime';

function displayInstanceName(instance: AgentInstance): string {
  if (instance.runtime === 'hermes' && instance.id === 'obol-agent')
    return 'Stack agent';
  return instance.id;
}

function runtimeLabel(instance: AgentInstance): string {
  return instance.runtime === 'hermes' ? 'Hermes' : 'Agent';
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

// Dominant "primary agent" chrome — mint-tinted gradient wash + faint
// hexapod-style glow, per the New Agent Instances design. Sub-agents are
// rendered separately (AgentInstanceCard) and are deliberately leaner.
const PrimaryCard = styled(Box, {
  position: 'relative',
  overflow: 'hidden',
  background:
    'linear-gradient(180deg, rgba(47,228,171,0.05) 0%, rgba(47,228,171,0) 120px), $bg03',
  border: '1px solid rgba(47,228,171,0.22)',
  borderRadius: '$4',
  padding: '$xl',
});

const Glow = styled(Box, {
  position: 'absolute',
  top: -80,
  left: -60,
  width: 260,
  height: 260,
  background:
    'radial-gradient(circle, rgba(47,228,171,0.10) 0%, rgba(47,228,171,0) 70%)',
  pointerEvents: 'none',
});

const Inner = styled(Flex, {
  position: 'relative',
  flexDirection: 'column',
  gap: '$md',
});

const RuntimeBadge = styled(Text, {
  fontSize: 10,
  fontWeight: '$semibold',
  color: '$obolGreen',
  backgroundColor: tones.success.bg,
  border: `1px solid ${tones.success.border}`,
  borderRadius: '$1',
  padding: '2px 6px',
  lineHeight: 1.2,
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  whiteSpace: 'nowrap',
});

const PrimaryBadge = styled(Text, {
  fontSize: 10,
  fontWeight: '$semibold',
  color: '$obolGreen',
  backgroundColor: 'rgba(47,228,171,0.08)',
  border: '1px solid rgba(47,228,171,0.28)',
  borderRadius: '$1',
  padding: '2px 6px',
  lineHeight: 1.2,
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  whiteSpace: 'nowrap',
});

const HostRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  marginTop: 5,
  flexWrap: 'wrap',
});

const MonoText = styled(Text, {
  fontSize: '$2',
  color: '$body',
  fontFamily: 'monospace',
});

const Dot = styled(Box, {
  width: 3,
  height: 3,
  borderRadius: '$pill',
  backgroundColor: '$muted',
  flexShrink: 0,
});

const KeysBadge = styled(Text, {
  fontSize: 10,
  fontWeight: '$semibold',
  color: '$obolGreen',
  backgroundColor: tones.success.bg,
  border: `1px solid ${tones.success.border}`,
  borderRadius: '$1',
  padding: '1px 5px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
});

const StatStrip = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1.4fr) minmax(0, 1fr) minmax(0, 0.8fr)',
  backgroundColor: '$bg01',
  border: '1px solid $stroke',
  borderRadius: '$2',
  '@media (max-width: 900px)': {
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
  },
});

const StatCell = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xxs',
  padding: '$md $lg',
  minWidth: 0,
  borderRight: '1px solid $stroke',
  '&:last-child': { borderRight: 'none' },
});

const StatLabel = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  lineHeight: 1.2,
});

const StatValue = styled(Text, {
  fontSize: '$5',
  color: '$light',
  fontWeight: '$semibold',
  fontFamily: 'monospace',
  fontVariantNumeric: 'tabular-nums',
  lineHeight: 1.25,
});

const StatSub = styled(Text, {
  fontSize: 10,
  color: '$muted',
  lineHeight: 1.3,
});

interface HomeAgentCardProps {
  instance: AgentInstance;
  uptimeHours: number;
  /** Charged requests in the last 24h for this agent (from ServiceOffer usage). */
  requests24h?: number | null;
  onChat?: (instance: AgentInstance) => void;
}

export function HomeAgentCard({
  instance,
  uptimeHours,
  requests24h,
  onChat,
}: HomeAgentCardProps) {
  const { status, loading } = useAgentStatus(instance);
  const { metadata } = useAgentWalletMetadata(instance);

  const primaryAddress = metadata?.addresses?.[0]?.address ?? null;
  const { balances } = useWalletAssetBalances(primaryAddress);
  const sortedBalances = sortBalances(balances);

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

  const modelDisplay = status?.model
    ? status.model.replace(/^openai\//, '')
    : '—';

  const hasRequests = typeof requests24h === 'number' && requests24h > 0;

  const dashboardBaseUrl = instance.dashboardHostname
    ? `http://${instance.dashboardHostname}`
    : null;
  const dashboardUrl = dashboardBaseUrl
    ? withCurrentIngressPort(dashboardBaseUrl)
    : null;

  return (
    <PrimaryCard>
      <Glow aria-hidden="true" />
      <Inner>
        {/* Identity row */}
        <Flex css={{ alignItems: 'flex-start', gap: '$sm' }}>
          <Avatar alt={name} fallback={initial} size={40} />
          <Box css={{ flex: 1, minWidth: 0 }}>
            <Flex css={{ alignItems: 'center', gap: '$xxs', flexWrap: 'wrap' }}>
              <Text
                css={{ fontSize: '$6', fontWeight: '$bold', color: '$light' }}
              >
                {name}
              </Text>
              <RuntimeBadge>{runtimeLabel(instance)}</RuntimeBadge>
              <PrimaryBadge>Primary</PrimaryBadge>
            </Flex>
            <HostRow>
              <MonoText
                css={{
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  maxWidth: '100%',
                }}
              >
                {instance.hostname}
              </MonoText>
              {primaryAddress && (
                <>
                  <Dot />
                  <Flex css={{ alignItems: 'center', gap: '$xxs' }}>
                    <MonoText>{truncateAddress(primaryAddress)}</MonoText>
                    <CopyButton
                      text={primaryAddress}
                      title="Copy wallet address"
                    />
                    {metadata && metadata.count > 1 && (
                      <KeysBadge>+{metadata.count - 1} keys</KeysBadge>
                    )}
                  </Flex>
                </>
              )}
            </HostRow>
          </Box>
          <StatusPill status={statusPillStatus} label={statusLabel} />
        </Flex>

        {/* Metric strip */}
        <StatStrip>
          <StatCell>
            <StatLabel>Balance</StatLabel>
            {primaryAddress && sortedBalances.length > 0 ? (
              <WalletBalanceList
                walletAddress={primaryAddress}
                balances={sortedBalances}
                compact
              />
            ) : (
              <StatValue>{'—'}</StatValue>
            )}
          </StatCell>
          <StatCell>
            <StatLabel>Requests · 24h</StatLabel>
            <StatValue>
              {hasRequests ? requests24h!.toLocaleString() : '—'}
            </StatValue>
            <StatSub>{hasRequests ? 'charged' : 'no charged calls'}</StatSub>
          </StatCell>
          <StatCell>
            <StatLabel>Uptime</StatLabel>
            <StatValue>{formatUptime(uptimeHours)}</StatValue>
            <StatSub>continuous</StatSub>
          </StatCell>
        </StatStrip>

        {/* Primary model + actions */}
        <Flex css={{ alignItems: 'flex-end', gap: '$lg', flexWrap: 'wrap' }}>
          <Box css={{ display: 'flex', flexDirection: 'column', gap: '$xxs' }}>
            <StatLabel>Primary model</StatLabel>
            <Text
              css={{
                fontSize: '$3',
                color: '$light',
                fontFamily: 'monospace',
                wordBreak: 'break-word',
              }}
            >
              {modelDisplay}
            </Text>
          </Box>
          <Box css={{ flex: 1 }} />
          <Flex css={{ alignItems: 'center', gap: '$md' }}>
            <Button
              variant="primary"
              size="sm"
              disabled={displayStatus !== 'running'}
              onClick={() => onChat?.(instance)}
            >
              Chat
            </Button>
            {dashboardUrl && (
              <Link
                href={dashboardUrl}
                target="_blank"
                rel="noopener noreferrer"
                css={{
                  fontSize: '$3',
                  fontWeight: '$semibold',
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: '$xxxs',
                }}
              >
                Dashboard <OpenInNew size="sm" />
              </Link>
            )}
          </Flex>
        </Flex>
      </Inner>
    </PrimaryCard>
  );
}
