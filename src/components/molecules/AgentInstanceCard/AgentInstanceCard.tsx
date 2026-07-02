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
import { Button as BaseButton } from '@/components/atoms/Button';
import { tones } from '@/lib/ui/tones';
import { StatusPill } from '../../atoms/StatusIndicator';
import { CopyButton } from '../../atoms/CopyButton/CopyButton';
import { SellAgentModal } from '../SellAgentModal';
import { WalletBalanceList } from '../WalletBalanceList';
import {
  useAgentStatus,
  useAgentWalletMetadata,
  useTunnelURL,
} from '../../../hooks/useAgents';
import { useWalletAssetBalances } from '../../../hooks/useWalletAssetBalances';
import { sortBalances } from '@/lib/wallet/sort-balances';
import type { AgentInstance } from '@/types/agent';
import { truncateAddress } from '@/utils/address';

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
    return 'Stack agent';
  }
  if (instance.runtime === 'openclaw' && instance.id === 'default') {
    return 'OpenClaw';
  }
  return instance.id;
}

function runtimeLabel(instance: AgentInstance): string {
  if (instance.isSubAgent) return 'Agent';
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
  onRefresh?: () => void;
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
  backgroundColor: tones.neutral.bg,
  border: `1px solid ${tones.neutral.border}`,
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
  backgroundColor: tones.success.bg,
  border: `1px solid ${tones.success.border}`,
  borderRadius: '4px',
  padding: '1px 5px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
});

const ActionsRow = styled(Flex, {
  paddingTop: '$md',
  alignItems: 'center',
  gap: '$xs',
  flexWrap: 'wrap',
});

// Bare Button plus chevron rotation; zero horizontal padding keeps the
// toggle flush with the card's text column.
const DetailsToggle = styled(BaseButton, {
  gap: '$xxxs',
  marginTop: '$md',
  paddingLeft: 0,
  paddingRight: 0,

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

// Success-toned pill CTA. Kept pill-shaped (not rectangular) so it stays
// visually paired with the ListingLink anchor beside it.
const SellButton = styled(BaseButton, {
  background: tones.success.bg,
  borderColor: tones.success.border,
  color: tones.success.text,

  '&:hover:not(:disabled)': {
    background: tones.success.bg,
    borderColor: tones.success.text,
    color: tones.success.text,
  },
});

// Anchor twin of SellButton — mirrors the Button atom's sm/pill metrics.
const ListingLink = styled('a', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxs',
  height: 28,
  paddingLeft: '$xs',
  paddingRight: '$xs',
  borderRadius: '$pill',
  border: `1px solid ${tones.success.border}`,
  background: tones.success.bg,
  color: tones.success.text,
  fontSize: '$1',
  fontWeight: '$semibold',
  lineHeight: 1,
  textDecoration: 'none',
  cursor: 'pointer',
  whiteSpace: 'nowrap',
  transition: 'background-color 150ms ease, border-color 150ms ease',

  '&:hover': {
    borderColor: tones.success.text,
  },

  '&:focus-visible': {
    outline: '2px solid $obolGreen',
    outlineOffset: 2,
  },
});

export function AgentInstanceCard({
  instance,
  onSelect,
  onRefresh,
}: AgentInstanceCardProps) {
  const { status, loading } = useAgentStatus(instance);
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

  const dashboardBaseUrl = instance.isSubAgent
    ? null
    : instance.runtime === 'openclaw'
      ? tunnelURL
        ? tunnelURL
        : `http://${instance.hostname}`
      : instance.dashboardHostname
        ? `http://${instance.dashboardHostname}`
        : null;

  const dashboardUrl = dashboardBaseUrl
    ? withCurrentIngressPort(dashboardBaseUrl)
    : null;

  const canSell =
    instance.isSubAgent === true &&
    !instance.listing &&
    displayStatus === 'running';

  const listing = instance.listing;
  const listingChipLabel =
    listing?.phase === 'live'
      ? 'On sale'
      : listing?.phase === 'draining'
        ? 'Draining'
        : listing
          ? 'Setting up'
          : null;
  const listingChipStatus =
    listing?.phase === 'live'
      ? 'synced'
      : listing?.phase === 'draining'
        ? 'syncing'
        : listing
          ? 'syncing'
          : 'unsynced';

  const provider = status?.model ? inferProvider(status.model) : null;

  return (
    <CardContainer id={`agent-${instance.runtime}-${instance.id}`}>
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
        {listingChipLabel ? (
          <StatusPill status={listingChipStatus} label={listingChipLabel} />
        ) : null}
      </TopRow>

      {primaryAddress && (
        <>
          <WalletHeaderRow>
            <WalletLabel>Wallet:</WalletLabel>
            <WalletAddressText>
              {truncateAddress(primaryAddress)}
            </WalletAddressText>
            <CopyButton text={primaryAddress} title="Copy address" />
            {metadata && metadata.count > 1 && (
              <KeysBadge>+{metadata.count - 1} keys</KeysBadge>
            )}
          </WalletHeaderRow>

          <WalletBalanceList
            walletAddress={primaryAddress}
            balances={sortedBalances}
          />
        </>
      )}

      {/* Sub-agents without a dashboard have no actions — skip the row so
          its padding doesn't leave a dead gap above Model Details. */}
      {(!instance.isSubAgent || dashboardUrl) && (
        <ActionsRow>
          {!instance.isSubAgent ? (
            <Button
              disabled={displayStatus !== 'running'}
              color="primary"
              onClick={() => onSelect?.(instance)}
              css={{ fontSize: '$3', height: '$sm' }}
            >
              Chat
            </Button>
          ) : null}

          {dashboardUrl ? (
            <Link
              href={dashboardUrl}
              target="_blank"
              rel="noopener noreferrer"
              css={{
                fontSize: '13px',
              }}
            >
              Dashboard
              <OpenInNew size="sm" />
            </Link>
          ) : null}
        </ActionsRow>
      )}

      <DetailsToggle
        type="button"
        variant="bare"
        size="sm"
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
                {instance.isSubAgent
                  ? status.model && status.model !== 'unknown'
                    ? `${provider ?? 'Custom'} · ${displayModelName(status.model)}`
                    : loading
                      ? 'Loading...'
                      : 'Cluster-internal (no public API check)'
                  : status.apiConnected === undefined
                    ? 'Checking...'
                    : status.apiConnected
                      ? `${provider ?? 'Unknown'} · connected`
                      : `${provider ?? 'Unknown'} · unreachable${
                          status.apiError ? ` — ${status.apiError}` : ''
                        }`}
              </DetailValue>
            </DetailRow>
          )}
        </DetailsBlock>
      )}

      {instance.isSubAgent ? (
        <>
          <SellDivider />
          <SellCtaRow>
            <SellLabel>
              {listing
                ? listing.phase === 'setting_up'
                  ? 'Going on sale…'
                  : 'On sale'
                : 'Sell this agent'}
            </SellLabel>
            {canSell ? (
              <SellAgentModal
                agentName={instance.id}
                agentNamespace={instance.namespace}
                defaultWallet={primaryAddress ?? undefined}
                onListed={onRefresh}
                trigger={
                  <SellButton type="button" variant="ghost" size="sm" pill>
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
            ) : listing ? (
              listing.phase !== 'setting_up' && listing.endpoint ? (
                // Live sale: open the actual x402-gated storefront URL.
                <ListingLink
                  href={listing.endpoint}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  View on storefront
                  <OpenInNew size="sm" />
                </ListingLink>
              ) : (
                <ListingLink
                  href={`#${listing.anchorId}`}
                  onClick={() => onRefresh?.()}
                >
                  {listing.phase === 'setting_up'
                    ? 'Finish setup'
                    : 'View on storefront'}
                </ListingLink>
              )
            ) : (
              <SellButton
                type="button"
                variant="ghost"
                size="sm"
                pill
                disabled
                title="Agent must be running"
              >
                Sell
              </SellButton>
            )}
          </SellCtaRow>
        </>
      ) : null}
    </CardContainer>
  );
}
