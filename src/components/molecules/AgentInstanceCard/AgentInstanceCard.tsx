'use client';

import { useState } from 'react';
import { Box, Flex, Text, styled, OpenInNew } from '@obolnetwork/obol-ui';
import { ChevronRightIcon } from '@radix-ui/react-icons';
import { Button as BaseButton } from '@/components/atoms/Button';
import { tones } from '@/lib/ui/tones';
import { StatusPill } from '../../atoms/StatusIndicator';
import { CopyButton } from '../../atoms/CopyButton/CopyButton';
import { Identicon } from '@/components/marketplace/Identicon';
import { SellAgentModal } from '../SellAgentModal';
import {
  useAgentStatus,
  useAgentWalletMetadata,
} from '../../../hooks/useAgents';
import type { AgentInstance } from '@/types/agent';
import { truncateAddress } from '@/utils/address';
import { palette } from '@/lib/ui/theme';

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
  return instance.runtime === 'hermes' ? 'Hermes' : 'OpenClaw';
}

interface AgentInstanceCardProps {
  instance: AgentInstance;
  onSelect?: (instance: AgentInstance) => void;
  onRefresh?: () => void;
}

const CardContainer = styled(Box, {
  padding: '$md $lg',
  borderRadius: '$2',
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  transition: 'border-color 0.2s ease',

  '&:hover': {
    borderColor: '$body',
  },
});

const HeaderRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  flexWrap: 'wrap',
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
  padding: '2px 6px',
  lineHeight: 1.2,
  whiteSpace: 'nowrap',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',

  variants: {
    tone: {
      violet: {
        color: palette.lavender,
        backgroundColor: 'rgba(145,103,228,0.12)',
        border: '1px solid rgba(145,103,228,0.34)',
      },
    },
  },
});

// Row that carries the Model Details toggle and the Sell CTA; indented to
// align under the identicon + name column, matching the design.
const ControlRow = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
  marginTop: '$md',
  paddingLeft: 38,
  flexWrap: 'wrap',
});

const DetailsToggle = styled(BaseButton, {
  gap: '$xxxs',
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
  marginTop: '$sm',
  paddingLeft: 38,
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const DetailRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xxs',
  flexWrap: 'wrap',
});

const DetailLabel = styled(Text, {
  fontSize: '$2',
  color: '$muted',
});

const DetailValue = styled(Text, {
  fontSize: '$2',
  color: '$light',
  fontFamily: 'monospace',
});

// Success-toned pill CTA for opening the sell flow. Styled as a plain button
// (not the Button atom) so its green fill can't be overridden by the atom's
// variant styles — kept visually identical to the ListingLink below.
const SellButton = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxs',
  height: 30,
  paddingLeft: '$sm',
  paddingRight: '$sm',
  borderRadius: '$pill',
  border: '1px solid rgba(47,228,171,0.42)',
  background: 'rgba(47,228,171,0.12)',
  color: '$obolGreen',
  fontFamily: '$body',
  fontSize: '$2',
  fontWeight: '$semibold',
  lineHeight: 1,
  whiteSpace: 'nowrap',
  cursor: 'pointer',
  transition: 'background-color 150ms ease, border-color 150ms ease',

  '&:hover': { background: 'rgba(47,228,171,0.18)' },
  '&:focus-visible': { outline: '2px solid $obolGreen', outlineOffset: 2 },
  '&:disabled': { opacity: 0.5, cursor: 'not-allowed' },
});

// Prominent storefront callout — shown only for listed agents.
const StorefrontFooter = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
  marginTop: '$md',
  padding: '12px 16px',
  borderRadius: '$2',
  background: 'rgba(47,228,171,0.06)',
  border: '1px solid rgba(47,228,171,0.22)',
  flexWrap: 'wrap',
});

const ListingLink = styled('a', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxs',
  height: 30,
  paddingLeft: '$sm',
  paddingRight: '$sm',
  borderRadius: '$pill',
  border: '1px solid rgba(47,228,171,0.42)',
  background: 'rgba(47,228,171,0.12)',
  color: '$obolGreen',
  fontSize: '$2',
  fontWeight: '$semibold',
  lineHeight: 1,
  textDecoration: 'none',
  cursor: 'pointer',
  whiteSpace: 'nowrap',
  transition: 'background-color 150ms ease, border-color 150ms ease',

  '&:hover': {
    background: 'rgba(47,228,171,0.18)',
  },

  '&:focus-visible': {
    outline: '2px solid $obolGreen',
    outlineOffset: 2,
  },
});

const StoreIcon = () => (
  <svg
    width="16"
    height="16"
    viewBox="0 0 16 16"
    fill="none"
    aria-hidden="true"
  >
    <path
      d="M2.5 6.2 3.4 3h9.2l.9 3.2"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinejoin="round"
    />
    <path
      d="M2.5 6.2v.3a1.7 1.7 0 0 0 3.4 0 1.7 1.7 0 0 0 3.4 0 1.7 1.7 0 0 0 3.4 0v-.3"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinejoin="round"
    />
    <path
      d="M3.4 8.4V13h9.2V8.4"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinejoin="round"
    />
  </svg>
);

export function AgentInstanceCard({
  instance,
  onRefresh,
}: AgentInstanceCardProps) {
  const { status, loading } = useAgentStatus(instance);
  const { metadata } = useAgentWalletMetadata(instance);

  const primaryAddress = metadata?.addresses?.[0]?.address ?? null;

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

  const canSell = !instance.listing && displayStatus === 'running';

  const listing = instance.listing;
  const listingChipLabel =
    listing?.phase === 'live'
      ? 'Listed'
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
  const identiconSeed = primaryAddress ?? `${instance.runtime}:${instance.id}`;

  return (
    <CardContainer id={`agent-${instance.runtime}-${instance.id}`}>
      <HeaderRow>
        <Identicon seed={identiconSeed} size={26} rounded={6} />
        <InstanceName>{displayInstanceName(instance)}</InstanceName>
        <RuntimeBadge
          tone={instance.runtime === 'openclaw' ? 'violet' : undefined}
        >
          {runtimeLabel(instance)}
        </RuntimeBadge>
        <Box css={{ flex: 1 }} />
        <Flex css={{ alignItems: 'center', gap: '$xxs' }}>
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
        </Flex>
      </HeaderRow>

      <ControlRow>
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
        <Box css={{ flex: 1 }} />
        {canSell && (
          <SellAgentModal
            agentName={instance.id}
            agentNamespace={instance.namespace}
            defaultWallet={primaryAddress ?? undefined}
            onListed={onRefresh}
            trigger={
              <SellButton type="button">
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
                Sell access
              </SellButton>
            }
          />
        )}
      </ControlRow>

      {detailsOpen && (
        <DetailsBlock>
          <DetailRow>
            <DetailLabel>Host:</DetailLabel>
            <DetailValue>{instance.hostname}</DetailValue>
          </DetailRow>

          {!loading && status?.model && (
            <DetailRow>
              <DetailLabel>Model:</DetailLabel>
              <DetailValue>
                {provider ? `${provider} · ` : ''}
                {displayModelName(status.model)}
              </DetailValue>
            </DetailRow>
          )}

          {primaryAddress && (
            <DetailRow>
              <DetailLabel>Wallet:</DetailLabel>
              <DetailValue>{truncateAddress(primaryAddress)}</DetailValue>
              <CopyButton text={primaryAddress} title="Copy address" />
              {metadata && metadata.count > 1 && (
                <DetailLabel>+{metadata.count - 1} keys</DetailLabel>
              )}
            </DetailRow>
          )}
        </DetailsBlock>
      )}

      {listing && (
        <StorefrontFooter>
          <Box css={{ color: '$obolGreen', display: 'inline-flex' }}>
            <StoreIcon />
          </Box>
          <Box css={{ minWidth: 0 }}>
            <Text
              css={{ fontSize: '$2', fontWeight: '$semibold', color: '$light' }}
            >
              {listing.phase === 'live'
                ? 'Listed on storefront'
                : listing.phase === 'draining'
                  ? 'Draining listing'
                  : 'Going on sale…'}
            </Text>
            <Text css={{ fontSize: '$1', color: '$muted', marginTop: 1 }}>
              {listing.phase === 'live'
                ? 'Public · earning from calls'
                : listing.phase === 'draining'
                  ? 'Winding down'
                  : 'Finishing setup'}
            </Text>
          </Box>
          <Box css={{ flex: 1 }} />
          {listing.phase !== 'setting_up' && listing.endpoint ? (
            <ListingLink
              href={listing.endpoint}
              target="_blank"
              rel="noopener noreferrer"
            >
              View listing <OpenInNew size="sm" />
            </ListingLink>
          ) : (
            <ListingLink
              href={`#${listing.anchorId}`}
              onClick={() => onRefresh?.()}
            >
              {listing.phase === 'setting_up' ? 'Finish setup' : 'View listing'}
            </ListingLink>
          )}
        </StorefrontFooter>
      )}
    </CardContainer>
  );
}
