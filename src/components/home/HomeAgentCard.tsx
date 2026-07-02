'use client';

import { Box, Flex, Text, Link, styled, OpenInNew } from '@obolnetwork/obol-ui';
import { Button } from '@/components/atoms/Button';
import { Card } from '../atoms/Card';
import { tones } from '../../lib/ui/tones';
import { StatusPill } from '../atoms/StatusIndicator';
import { Avatar } from '../atoms/Avatar';
import { CopyButton } from '../atoms/CopyButton';
import { WalletBalanceList } from '../molecules/WalletBalanceList';
import {
  useAgentStatus,
  useTunnelURL,
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
  if (instance.runtime === 'openclaw' && instance.id === 'default')
    return 'OpenClaw';
  return instance.id;
}

function runtimeLabel(instance: AgentInstance): string {
  return instance.runtime === 'hermes' ? 'Hermes' : 'OpenClaw';
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

// Tier 2 card chrome comes from the Card atom (`nested`); only the internal
// flex layout and hover affordance are defined here.
const cardLayoutCss = {
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
  transition: 'border-color 200ms ease',
  '&:hover': { borderColor: '$body' },
} as const;

const RuntimeBadge = styled(Text, {
  fontSize: 10,
  fontWeight: '$semibold',
  color: '$body',
  backgroundColor: tones.neutral.bg,
  border: `1px solid ${tones.neutral.border}`,
  borderRadius: '$1',
  padding: '2px 6px',
  lineHeight: 1.2,
  textTransform: 'uppercase',
});

const StatStrip = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1.4fr) minmax(0, 1fr) minmax(0, 0.9fr)',
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
  alignItems: 'flex-start',
  gap: '$xxs',
  padding: '$sm $md',
  minWidth: 0,
});

const StatLabel = styled(Text, {
  fontSize: '$1',
  fontWeight: '$medium',
  color: '$muted',
  lineHeight: 1.2,
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
});

const StatValue = styled(Text, {
  fontSize: '$3',
  color: '$light',
  fontWeight: '$semibold',
  fontFamily: 'monospace',
  lineHeight: 1.25,
  width: '100%',
});

const StatSub = styled(Text, {
  fontSize: 10,
  color: '$muted',
  lineHeight: 1.3,
});

const WalletInlineRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xxs',
  width: '100%',
  minHeight: 22,
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
    <Card nested css={cardLayoutCss}>
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

      <StatStrip>
        <StatCell>
          <StatLabel>Wallet</StatLabel>
          {primaryAddress ? (
            <>
              <WalletInlineRow>
                <StatValue css={{ fontSize: '$2', flex: 1, width: 'auto' }}>
                  {truncateAddress(primaryAddress)}
                </StatValue>
                <CopyButton text={primaryAddress} title="Copy address" />
                {metadata && metadata.count > 1 && (
                  <KeysBadge>+{metadata.count - 1} keys</KeysBadge>
                )}
              </WalletInlineRow>
              <WalletBalanceList
                walletAddress={primaryAddress}
                balances={sortedBalances}
                compact
              />
            </>
          ) : (
            <StatValue>{'\u2014'}</StatValue>
          )}
        </StatCell>
        <StatCell>
          <StatLabel>Primary model</StatLabel>
          <StatValue
            css={{
              fontSize: '$2',
              wordBreak: 'break-word',
              whiteSpace: 'normal',
            }}
          >
            {modelDisplay}
          </StatValue>
        </StatCell>
        <StatCell>
          <StatLabel>Uptime</StatLabel>
          <StatValue>{formatUptime(uptimeHours)}</StatValue>
          <StatSub>continuous</StatSub>
        </StatCell>
      </StatStrip>

      <Flex css={{ alignItems: 'center', gap: '$xs' }}>
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
    </Card>
  );
}
