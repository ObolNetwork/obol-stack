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
import { CopyButton } from '../../atoms/CopyButton/CopyButton';
import { SellInferenceModal } from '../SellInferenceModal';
import {
  useAgentStatus,
  useAgentToken,
  useAgentWalletMetadata,
  useTunnelURL,
} from '../../../hooks/useAgents';
import { useAddressBalance } from '../../../hooks/useAddressBalance';
import type { AgentInstance } from '@/types/agent';

function displayModelName(raw: string): string {
  return raw.replace(/^openai\//, '');
}

function inferProvider(modelName: string): string {
  const name = displayModelName(modelName);
  if (name.includes('claude')) return 'Anthropic';
  if (/^(gpt|o[1-4])/.test(name)) return 'OpenAI';
  if (name === 'hermes-agent') return 'Hermes';
  return 'Ollama';
}

function displayInstanceName(instance: AgentInstance): string {
  if (instance.runtime === 'hermes' && instance.id === 'obol-agent') {
    return 'Obol Agent';
  }
  if (instance.runtime === 'openclaw' && instance.id === 'default') {
    return 'OpenClaw';
  }
  return instance.id;
}

function runtimeLabel(instance: AgentInstance): string {
  return instance.runtime === 'hermes' ? 'Hermes' : 'OpenClaw';
}

function withCurrentIngressPort(rawUrl: string): string {
  if (typeof window === 'undefined') {
    return rawUrl;
  }

  try {
    const target = new URL(rawUrl);
    const current = new URL(window.location.href);

    if (
      target.hostname.endsWith('.obol.stack') &&
      current.hostname.endsWith('obol.stack')
    ) {
      target.protocol = current.protocol;
      if (!target.port && current.port) {
        target.port = current.port;
      }
    }

    return target.toString().replace(/\/$/, '');
  } catch {
    return rawUrl;
  }
}

interface AgentInstanceCardProps {
  instance: AgentInstance;
  onSelect?: (instance: AgentInstance) => void;
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

const RuntimeBadge = styled(Text, {
  fontSize: '10px',
  fontWeight: '$semibold',
  color: '$body',
  backgroundColor: 'rgba(255, 255, 255, 0.05)',
  border: '1px solid rgba(255, 255, 255, 0.12)',
  borderRadius: '4px',
  padding: '1px 5px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
  textTransform: 'uppercase',
});

const MetaItem = styled(Flex, {
  alignItems: 'center',
  gap: '$xxxs',
  marginBottom: '$xxs',
  marginTop: '$xs',
  flexWrap: 'wrap',
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
  flexWrap: 'wrap',
});

const BalanceText = styled(Text, {
  fontSize: '$1',
  color: '$body',
  whiteSpace: 'nowrap',
});

const KeysBadge = styled(Text, {
  fontSize: '10px',
  fontWeight: '$semibold',
  color: '$obolGreen',
  backgroundColor: 'rgba(47, 228, 171, 0.08)',
  border: '1px solid rgba(47, 228, 171, 0.25)',
  borderRadius: '4px',
  padding: '1px 5px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
});

const SellDivider = styled(Box, {
  height: '1px',
  backgroundColor: '$stroke',
  marginTop: '$md',
});

const SellCtaRow = styled(Flex, {
  paddingTop: '$md',
  alignItems: 'center',
  justifyContent: 'space-between',
});

const SellLabel = styled(Text, {
  fontSize: '$2',
  color: '$body',
  lineHeight: 1.4,
});

const SellButton = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  padding: '$xxxs $sm',
  borderRadius: '$pill',
  border: '1px solid rgba(47, 228, 171, 0.3)',
  background: 'rgba(47, 228, 171, 0.08)',
  color: '#2FE4AB',
  fontSize: '$2',
  fontWeight: '$semibold',
  cursor: 'pointer',
  transition: 'all 0.15s ease',
  whiteSpace: 'nowrap',

  '&:hover': {
    background: 'rgba(47, 228, 171, 0.15)',
    borderColor: 'rgba(47, 228, 171, 0.5)',
  },
});

function truncateAddress(address: string): string {
  if (address.length <= 10) return address;
  return `${address.slice(0, 6)}...${address.slice(-4)}`;
}

export function AgentInstanceCard({
  instance,
  onSelect,
}: AgentInstanceCardProps) {
  const { status, loading } = useAgentStatus(instance);
  const { token, tokenError } = useAgentToken(
    instance.runtime === 'openclaw' ? instance : null
  );
  const { metadata } = useAgentWalletMetadata(instance);
  const { tunnelURL } = useTunnelURL();

  const primaryAddress = metadata?.addresses?.[0]?.address ?? null;
  const { balance: balanceInfo } = useAddressBalance(primaryAddress);

  const displayStatus = loading
    ? 'pending'
    : status?.podStatus?.toLowerCase() === 'running'
      ? 'running'
      : status?.podStatus?.toLowerCase() === 'pending'
        ? 'pending'
        : status
          ? 'error'
          : 'unknown';

  const dashboardBaseUrl =
    instance.runtime === 'openclaw'
      ? tunnelURL
        ? `${tunnelURL}${token ? `/#token=${token}` : ''}`
        : `http://${instance.hostname}${token ? `/#token=${token}` : ''}`
      : instance.dashboardHostname
        ? `http://${instance.dashboardHostname}`
        : null;

  const dashboardUrl = dashboardBaseUrl
    ? withCurrentIngressPort(dashboardBaseUrl)
    : null;

  const showSellCta =
    instance.runtime === 'openclaw' &&
    instance.id === 'default' &&
    displayStatus === 'running';

  return (
    <CardContainer>
      <TopRow>
        <Flex css={{ alignItems: 'center', gap: '$xs', flexWrap: 'wrap' }}>
          <InstanceName>{displayInstanceName(instance)}</InstanceName>
          <RuntimeBadge>{runtimeLabel(instance)}</RuntimeBadge>
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
          <MetaValue>{displayModelName(status.model)}</MetaValue>
        </MetaItem>
      )}

      {!loading && status && (
        <MetaItem>
          <MetaLabel>API:</MetaLabel>
          <MetaValue>
            {status.apiConnected === undefined
              ? 'Checking...'
              : status.apiConnected
                ? `Connected${status.model ? ` (${inferProvider(status.model)})` : ''}`
                : `Error${status.apiError ? ` — ${status.apiError}` : ''}`}
          </MetaValue>
        </MetaItem>
      )}

      {metadata && metadata.addresses.length > 0 && (
        <MetaItem>
          <MetaLabel>Wallet:</MetaLabel>
          <MetaValue>
            {truncateAddress(metadata.addresses[0].address)}
          </MetaValue>
          <CopyButton
            text={metadata.addresses[0].address}
            title="Copy address"
          />
          {metadata.count > 1 && (
            <KeysBadge>+{metadata.count - 1} keys</KeysBadge>
          )}
          {balanceInfo && (
            <BalanceText>
              {balanceInfo.balance} ETH ({balanceInfo.network})
            </BalanceText>
          )}
        </MetaItem>
      )}

      {tokenError && instance.runtime === 'openclaw' && (
        <MetaItem>
          <MetaLabel css={{ color: '$error' }}>Token:</MetaLabel>
          <MetaValue css={{ color: '$error' }}>
            {tokenError} — run: obol openclaw token --regenerate
          </MetaValue>
        </MetaItem>
      )}

      <ActionsRow>
        <Button
          disabled={displayStatus !== 'running'}
          color="primary"
          onClick={() => onSelect?.(instance)}
          css={{ fontSize: '$3', height: '$sm' }}
        >
          Chat
        </Button>

        {dashboardUrl ? (
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
        ) : null}
      </ActionsRow>

      {showSellCta && (
        <>
          <SellDivider />
          <SellCtaRow>
            <SellLabel>Sell access to your models</SellLabel>
            <SellInferenceModal
              defaultWallet={primaryAddress ?? undefined}
              trigger={
                <SellButton>
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <circle cx="12" cy="12" r="10" />
                    <line x1="12" y1="8" x2="12" y2="16" />
                    <line x1="8" y1="12" x2="16" y2="12" />
                  </svg>
                  Sell
                </SellButton>
              }
            />
          </SellCtaRow>
        </>
      )}
    </CardContainer>
  );
}
