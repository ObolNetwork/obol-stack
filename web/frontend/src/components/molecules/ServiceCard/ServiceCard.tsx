'use client';

import { useEffect, useState, type MouseEvent } from 'react';
import {
  Box,
  Button,
  Flex,
  Text,
  Link,
  styled,
  OpenInNew,
} from '@obolnetwork/obol-ui';
import { StatusPill } from '../../atoms/StatusIndicator';
import { Tooltip } from '../../atoms/Tooltip';
import { MetricCell } from '../../atoms/MetricCell';
import { ChainIcon } from '../../atoms/ChainIcon';
import { CopyButton } from '../../atoms/CopyButton/CopyButton';
import { ObolAmountSwitch } from '../../atoms/ObolAmountSwitch';
import { ExpandableChipList } from '../ExpandableChipList';
import { ServiceReadinessRow } from '../ServiceReadinessRow';
import { StopDrainModal } from '../StopDrainModal';
import {
  serviceOfferAssetSymbol,
  serviceOfferTrustBadges,
} from '@/lib/services/service-offer-display';
import {
  deriveOfferDisplayStatus,
  registeredConditionMessage,
} from '@/lib/services/offer-status';
import { publicServiceEndpoint } from '@/lib/services/public-storefront';
import {
  formatRelativeAgo,
  formatUsageDisplay,
} from '@/lib/services/service-usage';
import { toChainName } from '@/lib/marketplace/chain';
import { truncateAddress } from '@/utils/address';
import type { ChainName } from '@/types/dashboard';
import type {
  ServiceOffer,
  ServiceOfferAgentLink,
  ServiceOfferCondition,
  ServiceOfferType,
} from '@/types/sell';
import { palette } from '@/lib/ui/theme';

interface ServiceCardProps {
  offer: ServiceOffer;
  tunnelURL: string | null;
  onChanged?: () => void | Promise<void>;
  anchorId?: string;
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

const TopRow = styled('div', {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  width: '100%',
  marginBottom: '$xs',
  gap: '$sm',

  '@sm': { flexWrap: 'wrap' },
});

const HeaderMeta = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: '$xs',
  flexWrap: 'wrap',
  flex: '1 1 auto',
  minWidth: 0,
});

const HeaderStatus = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: '$xs',
  flexShrink: 0,
  marginLeft: 'auto',
});

const ServiceName = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
  overflowWrap: 'anywhere',
});

const TypeBadge = styled(Text, {
  fontSize: '$1',
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

const Description = styled(Text, {
  fontSize: '$2',
  color: '$body',
  marginBottom: '$sm',
  lineHeight: 1.4,
});

const ObjectiveLabel = styled('span', {
  color: '$muted',
});

// Chip pairing the chain logo with its human name (raw string fallback).
const NetworkPill = styled('span', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxxs',
  fontSize: '$1',
  color: '$body',
  backgroundColor: 'rgba(255, 255, 255, 0.04)',
  border: '1px solid rgba(255, 255, 255, 0.08)',
  borderRadius: '$pill',
  padding: '1px 8px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
  flexShrink: 0,
});

const InfoRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xxxs',
  flexWrap: 'wrap',
  marginTop: '$xs',
});

const PriceRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xxxs',
  flexWrap: 'nowrap',
  marginTop: '$xs',
  width: '100%',

  '@sm': { flexWrap: 'wrap' },
});

const InfoLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
});

const InfoValue = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
});

const PriceValue = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
  minWidth: 0,
});

const MetaRow = styled(Flex, {
  flexDirection: 'column',
  gap: '$sm',
  marginTop: '$sm',
});

const TrafficRow = styled(Flex, {
  alignItems: 'stretch',
  gap: '$sm',
  flexWrap: 'wrap',
  marginTop: '$xs',
});

const SkillChip = styled(Text, {
  fontSize: '$1',
  color: '$body',
  backgroundColor: 'rgba(255, 255, 255, 0.04)',
  border: '1px solid rgba(255, 255, 255, 0.08)',
  borderRadius: '4px',
  padding: '1px 6px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
});

const DrainingBadge = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  color: palette.gold,
  backgroundColor: 'rgba(245, 197, 66, 0.1)',
  border: '1px solid rgba(245, 197, 66, 0.35)',
  borderRadius: '4px',
  padding: '1px 5px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
  fontVariantNumeric: 'tabular-nums',
});

const UsageBadge = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  color: '$obolGreen',
  backgroundColor: 'rgba(47, 228, 171, 0.08)',
  border: '1px solid rgba(47, 228, 171, 0.25)',
  borderRadius: '4px',
  padding: '1px 5px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
  fontVariantNumeric: 'tabular-nums',
});

const AgentLinkText = styled('a', {
  fontSize: '$1',
  color: '$obolGreen',
  fontFamily: 'monospace',
  textDecoration: 'none',
  cursor: 'pointer',
  '&:hover': { textDecoration: 'underline' },
});

const TYPE_LABEL: Record<ServiceOfferType, string> = {
  agent: 'Agent',
  inference: 'Inference',
  'fine-tuning': 'Fine-tuning',
  http: 'HTTP',
};

const CHAIN_DISPLAY_NAME: Record<ChainName, string> = {
  mainnet: 'Ethereum',
  sepolia: 'Sepolia',
  hoodi: 'Hoodi',
  base: 'Base',
  'base-sepolia': 'Base Sepolia',
  aztec: 'Aztec',
};

const SALES_7D_TOOLTIP =
  'Successful charged requests in the last 7 days (Prometheus / x402 verifier).';

function formatPrice(
  offer: ServiceOffer
): { count: string; unit: string } | null {
  const price = offer.payment?.price;
  if (!price) return null;
  if (price.perRequest)
    return { count: String(price.perRequest), unit: 'request' };
  if (price.perMTok) return { count: String(price.perMTok), unit: 'Mtok' };
  if (price.perHour) return { count: String(price.perHour), unit: 'hour' };
  if (price.perEpoch) return { count: String(price.perEpoch), unit: 'epoch' };
  return null;
}

function pluralizeSales(n: number): string {
  return n === 1 ? '1 sale' : `${n.toLocaleString()} sales`;
}

function agentDisplayName(link: ServiceOfferAgentLink): string {
  if (link.runtime === 'hermes' && link.id === 'obol-agent')
    return 'Stack agent';
  if (link.runtime === 'openclaw' && link.id === 'default') return 'OpenClaw';
  return link.id;
}

function scrollToAgent(link: ServiceOfferAgentLink) {
  const el = document.getElementById(`agent-${link.runtime}-${link.id}`);
  el?.scrollIntoView({ behavior: 'smooth', block: 'center' });
}

function drainEndsAt(
  conditions: ServiceOfferCondition[] | undefined
): Date | null {
  const c = conditions?.find(x => x.type === 'Draining' && x.status === 'True');
  if (!c) return null;
  const m = c.message?.match(
    /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})/
  );
  if (!m) return null;
  const date = new Date(m[0]);
  return Number.isNaN(date.getTime()) ? null : date;
}

function parseDrainEndsAt(value: string | undefined): Date | null {
  if (!value) return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function formatCountdown(target: Date, now: Date): string {
  const deltaMs = target.getTime() - now.getTime();
  if (deltaMs <= 0) return 'expiring';
  const totalSeconds = Math.floor(deltaMs / 1000);
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  const s = totalSeconds % 60;
  if (h > 0) return `${h}h ${m.toString().padStart(2, '0')}m`;
  if (m > 0) return `${m}m ${s.toString().padStart(2, '0')}s`;
  return `${s}s`;
}

function useNow(intervalMs = 1000, enabled = true): Date {
  const [now, setNow] = useState<Date>(() => new Date());
  useEffect(() => {
    if (!enabled) return;
    const id = setInterval(() => setNow(new Date()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs, enabled]);
  return now;
}

export function ServiceCard({
  offer,
  tunnelURL,
  onChanged,
  anchorId,
}: ServiceCardProps) {
  const displayName = offer.registration?.name || offer.name;
  const price = formatPrice(offer);
  const symbol = serviceOfferAssetSymbol(offer.payment);
  const network = offer.caip2Network || offer.payment?.network;
  const chainName = toChainName(network);
  const transferMethod = offer.payment?.asset?.transferMethod;
  const modelName = offer.model?.name || offer.agentResolution?.model;
  const skills =
    offer.registration?.skills && offer.registration.skills.length > 0
      ? offer.registration.skills
      : (offer.agentResolution?.skills ?? []);
  const domains = offer.registration?.domains ?? [];
  const trustBadges = serviceOfferTrustBadges(offer.registration);
  const chargedRequests = offer.usage?.chargedRequests;
  const requests24h = offer.usage?.requests24h;
  const lastSettlement = offer.usage?.lastSettlementUnix;
  const endpoint = publicServiceEndpoint(offer.endpoint, tunnelURL);

  const drainEnd =
    parseDrainEndsAt(offer.drainEndsAt) ?? drainEndsAt(offer.conditions);
  const isDraining = drainEnd !== null;
  const now = useNow(1000, isDraining);
  const countdown = drainEnd ? formatCountdown(drainEnd, now) : null;
  const displayStatus = deriveOfferDisplayStatus(offer, { isDraining });
  const registrationPendingTip =
    registeredConditionMessage(offer) ||
    'Serving paid traffic now. On-chain ERC-8004 registration is still pending — complete it with `obol sell register`.';

  return (
    <CardContainer id={anchorId}>
      <TopRow>
        <HeaderMeta>
          <ServiceName>{displayName}</ServiceName>
          <TypeBadge>{TYPE_LABEL[offer.type]}</TypeBadge>
          {modelName && (
            <TypeBadge
              css={{
                color: '$obolGreen',
                borderColor: 'rgba(47, 228, 171, 0.3)',
                background: 'rgba(47, 228, 171, 0.08)',
              }}
            >
              {modelName}
            </TypeBadge>
          )}
          {offer.isDemo && <TypeBadge>Demo</TypeBadge>}
          {transferMethod && <TypeBadge>{transferMethod}</TypeBadge>}
          {endpoint && (
            <Link
              href={endpoint}
              target="_blank"
              rel="noopener noreferrer"
              title="Open service in new tab"
              aria-label="Open service in new tab"
              css={{
                display: 'inline-flex',
                alignItems: 'center',
                fontSize: '13px',
              }}
            >
              <OpenInNew size="sm" />
            </Link>
          )}
        </HeaderMeta>
        <HeaderStatus>
          {isDraining && (
            <DrainingBadge title={`Drain ends at ${drainEnd?.toISOString()}`}>
              Draining{countdown ? ` · ends in ${countdown}` : ''}
            </DrainingBadge>
          )}
          {displayStatus === 'registration-pending' ? (
            <Tooltip content={registrationPendingTip}>
              <span style={{ display: 'inline-flex' }}>
                <StatusPill
                  status="synced"
                  label="Live · registration pending"
                />
              </span>
            </Tooltip>
          ) : (
            <StatusPill
              status={displayStatus === 'ready' ? 'synced' : 'syncing'}
              label={
                displayStatus === 'draining'
                  ? 'Draining'
                  : displayStatus === 'ready'
                    ? 'Ready'
                    : 'Pending'
              }
            />
          )}
        </HeaderStatus>
      </TopRow>

      {offer.registration?.description && (
        <Description>
          <ObjectiveLabel>Objective:</ObjectiveLabel>{' '}
          {offer.registration.description}
        </Description>
      )}

      {price && (
        <PriceRow>
          <InfoLabel>Price:</InfoLabel>
          <PriceValue>
            <ObolAmountSwitch
              amount={price.count}
              symbol={symbol}
              unit={`/ ${price.unit}`}
            />
          </PriceValue>
          {network && (
            <NetworkPill title={network}>
              {chainName && <ChainIcon chain={chainName} size={12} />}
              {chainName ? CHAIN_DISPLAY_NAME[chainName] : network}
            </NetworkPill>
          )}
        </PriceRow>
      )}

      <InfoRow>
        <InfoLabel>Fee recipient:</InfoLabel>
        {offer.agentLink ? (
          <AgentLinkText
            href={`#agent-${offer.agentLink.runtime}-${offer.agentLink.id}`}
            onClick={(e: MouseEvent<HTMLAnchorElement>) => {
              e.preventDefault();
              if (offer.agentLink) scrollToAgent(offer.agentLink);
            }}
          >
            {agentDisplayName(offer.agentLink)}
          </AgentLinkText>
        ) : offer.payment?.payTo ? (
          <>
            <InfoValue>{truncateAddress(offer.payment.payTo)}</InfoValue>
            <CopyButton text={offer.payment.payTo} title="Copy address" />
          </>
        ) : (
          <InfoValue>—</InfoValue>
        )}
        <Tooltip content={SALES_7D_TOOLTIP} delayDuration={400}>
          <span style={{ display: 'inline-flex' }}>
            <UsageBadge
              css={
                chargedRequests != null && chargedRequests > 0
                  ? undefined
                  : { opacity: 0.75, color: '$body', borderColor: '$stroke' }
              }
            >
              {chargedRequests == null ? '—' : pluralizeSales(chargedRequests)}
            </UsageBadge>
          </span>
        </Tooltip>
      </InfoRow>

      <TrafficRow>
        <MetricCell
          label="Requests (24h)"
          value={formatUsageDisplay(requests24h)}
        />
        <MetricCell
          label="Sales (7d)"
          value={formatUsageDisplay(chargedRequests)}
        />
        <MetricCell
          label="Last sale"
          value={
            lastSettlement != null && lastSettlement > 0
              ? formatRelativeAgo(lastSettlement)
              : '—'
          }
        />
      </TrafficRow>

      {endpoint && (
        <InfoRow>
          <InfoLabel>Public URL:</InfoLabel>
          <InfoValue css={{ wordBreak: 'break-all' }}>{endpoint}</InfoValue>
          <CopyButton text={endpoint} title="Copy public URL" />
        </InfoRow>
      )}

      <MetaRow>
        <ExpandableChipList items={skills} />
        <ExpandableChipList items={domains} labelPrefix="Domain: " />
        {trustBadges.length > 0 && (
          <Flex css={{ flexWrap: 'wrap', gap: '$xxs' }}>
            {trustBadges.map(method => (
              <SkillChip key={method} title="Supported trust method">
                Trust: {method}
              </SkillChip>
            ))}
          </Flex>
        )}
      </MetaRow>

      <ServiceReadinessRow offer={offer} />

      <Flex
        css={{
          marginTop: '$xs',
          justifyContent: 'flex-end',
          gap: '$xs',
          flexWrap: 'wrap',
        }}
      >
        {!isDraining && (
          <StopDrainModal
            name={offer.name}
            namespace={offer.namespace}
            onDrained={() => {
              void onChanged?.();
            }}
            trigger={
              <Button
                color="secondary"
                title="Take this service off your storefront; in-flight buyers get a grace window"
              >
                Stop selling
              </Button>
            }
          />
        )}
      </Flex>
    </CardContainer>
  );
}
