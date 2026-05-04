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
import { ChevronRightIcon } from '@radix-ui/react-icons';
import { StatusPill } from '../../atoms/StatusIndicator';
import { CopyButton } from '../../atoms/CopyButton/CopyButton';
import { SellInferenceModal } from '../SellInferenceModal';
import {
  useAgentStatus,
  useAgentToken,
  useAgentWalletMetadata,
  useTunnelURL,
} from '../../../hooks/useAgents';
import {
  useWalletAssetBalances,
  type AssetBalance,
} from '../../../hooks/useWalletAssetBalances';
import { explorerUrlFor } from '../../../lib/config/agent-balance-tokens';
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
  marginBottom: '$md',
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

const WalletHeaderRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xxxs',
  flexWrap: 'wrap',
  marginTop: '$xs',
});

const WalletLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
});

const WalletAddressText = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
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

const BalancesList = styled(Flex, {
  flexDirection: 'column',
  gap: '$xxxs',
  marginTop: '$xs',
});

const BalanceRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  fontSize: '$1',
});

const BalanceSymbol = styled(Text, {
  fontSize: '11px',
  fontWeight: '$semibold',
  color: '$light',
  fontFamily: 'monospace',
  minWidth: '38px',
});

const BalanceAmount = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
  flex: 1,
});

const ChainHint = styled(Text, {
  fontSize: '10px',
  color: '$body',
  textTransform: 'lowercase',
  whiteSpace: 'nowrap',
});

const ExplorerLink = styled('a', {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,
  width: 22,
  height: 22,
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: 'transparent',
  color: '$body',
  cursor: 'pointer',
  transition:
    'color 150ms ease, border-color 150ms ease, background 150ms ease',
  '&:hover': {
    color: '$light',
    borderColor: '$body',
    background: '$bg05',
  },
});

const ActionsRow = styled(Flex, {
  paddingTop: '$md',
  alignItems: 'center',
  gap: '$xs',
  flexWrap: 'wrap',
});

const DetailsToggle = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxxs',
  marginTop: '$md',
  padding: 0,
  background: 'transparent',
  border: 'none',
  color: '$body',
  fontSize: '$1',
  cursor: 'pointer',
  transition: 'color 150ms ease',
  '&:hover': { color: '$light' },

  '& svg': {
    transition: 'transform 150ms ease',
  },

  variants: {
    expanded: {
      true: {
        '& svg': { transform: 'rotate(90deg)' },
      },
    },
  },
});

const DetailsBlock = styled(Box, {
  marginTop: '$xs',
  paddingTop: '$xs',
  borderTop: '1px dashed $stroke',
});

const DetailRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xxxs',
  marginTop: '$xxs',
  flexWrap: 'wrap',
});

const DetailLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
});

const DetailValue = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
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

function sortBalances(balances: AssetBalance[]): AssetBalance[] {
  const symbolOrder: Record<string, number> = {
    ETH: 0,
    WETH: 1,
    OBOL: 2,
    USDC: 3,
  };
  return [...balances].sort((a, b) => {
    if (a.chain.chainId !== b.chain.chainId) {
      return a.chain.chainId - b.chain.chainId;
    }
    return (
      (symbolOrder[a.token.symbol] ?? 99) - (symbolOrder[b.token.symbol] ?? 99)
    );
  });
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
  const { balances } = useWalletAssetBalances(primaryAddress);
  const sortedBalances = sortBalances(balances);

  const [detailsOpen, setDetailsOpen] = useState(false);

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

  const provider = status?.model ? inferProvider(status.model) : null;
  const showOpenclawTokenError =
    Boolean(tokenError) && instance.runtime === 'openclaw';

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

      {primaryAddress && (
        <>
          <WalletHeaderRow>
            <WalletLabel>Wallet:</WalletLabel>
            <WalletAddressText>
              {truncateAddress(primaryAddress)}
            </WalletAddressText>
            <CopyButton text={primaryAddress} title="Copy address" />
            <ExplorerLink
              href={`https://etherscan.io/address/${primaryAddress}`}
              target="_blank"
              rel="noopener noreferrer"
              title="View on Etherscan"
            >
              <OpenInNew size="sm" />
            </ExplorerLink>
            {metadata && metadata.count > 1 && (
              <KeysBadge>+{metadata.count - 1} keys</KeysBadge>
            )}
          </WalletHeaderRow>

          {sortedBalances.length > 0 && (
            <BalancesList>
              {sortedBalances.map(b => {
                const key = `${b.chain.chainId}-${b.token.symbol}`;
                const url = explorerUrlFor(b.chain, b.token, primaryAddress);
                return (
                  <BalanceRow key={key}>
                    <BalanceSymbol>{b.token.symbol}</BalanceSymbol>
                    <BalanceAmount>{b.formatted}</BalanceAmount>
                    <ChainHint>{b.chain.shortName}</ChainHint>
                    {b.token.address && (
                      <CopyButton
                        text={b.token.address}
                        title={`Copy ${b.token.symbol} contract`}
                      />
                    )}
                    <ExplorerLink
                      href={url}
                      target="_blank"
                      rel="noopener noreferrer"
                      title={`View on ${b.chain.chainName} explorer`}
                    >
                      <OpenInNew size="sm" />
                    </ExplorerLink>
                  </BalanceRow>
                );
              })}
            </BalancesList>
          )}
        </>
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

      <DetailsToggle
        type="button"
        onClick={() => setDetailsOpen(o => !o)}
        expanded={detailsOpen}
        aria-expanded={detailsOpen}
      >
        <ChevronRightIcon width={14} height={14} />
        Model Details
      </DetailsToggle>

      {detailsOpen && (
        <DetailsBlock>
          <DetailRow>
            <DetailLabel>Host:</DetailLabel>
            <DetailValue>{instance.hostname}</DetailValue>
          </DetailRow>

          {!loading && status?.model && (
            <DetailRow>
              <DetailLabel>Model:</DetailLabel>
              <DetailValue>{displayModelName(status.model)}</DetailValue>
            </DetailRow>
          )}

          {!loading && status && (
            <DetailRow>
              <DetailLabel>Provider:</DetailLabel>
              <DetailValue>
                {status.apiConnected === undefined
                  ? 'Checking...'
                  : status.apiConnected
                    ? `${provider ?? 'Unknown'} · connected`
                    : `${provider ?? 'Unknown'} · unreachable${
                        status.apiError ? ` — ${status.apiError}` : ''
                      }`}
              </DetailValue>
            </DetailRow>
          )}

          {showOpenclawTokenError && (
            <DetailRow>
              <DetailLabel css={{ color: '$error' }}>Token:</DetailLabel>
              <DetailValue css={{ color: '$error' }}>
                {tokenError} — run: obol openclaw token --regenerate
              </DetailValue>
            </DetailRow>
          )}
        </DetailsBlock>
      )}

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
