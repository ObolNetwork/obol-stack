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
import { CopyButton } from '../../atoms/CopyButton/CopyButton';
import { ObolAmountSwitch } from '../../atoms/ObolAmountSwitch';
import { StopDrainModal } from '../StopDrainModal';
import { serviceOfferResourcePath } from '@/lib/services/sell-actions';
import {
  serviceOfferAssetSymbol,
  serviceOfferTrustBadges,
} from '@/lib/services/service-offer-display';
import type {
  ServiceOffer,
  ServiceOfferAgentLink,
  ServiceOfferCondition,
  ServiceOfferType,
} from '@/types/sell';

interface ServiceCardProps {
  offer: ServiceOffer;
  tunnelURL: string | null;
  onChanged?: () => void | Promise<void>;
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
  marginBottom: '$xs',
  gap: '$sm',
});

const ServiceName = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
});

const TypeBadge = styled(Text, {
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

const Description = styled(Text, {
  fontSize: '$2',
  color: '$body',
  marginBottom: '$sm',
  lineHeight: 1.4,
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
  flex: 1,
  minWidth: 0,
});

const SkillsRow = styled(Flex, {
  flexWrap: 'wrap',
  gap: '$xxs',
  marginTop: '$sm',
});

const SkillChip = styled(Text, {
  fontSize: '10px',
  color: '$body',
  backgroundColor: 'rgba(255, 255, 255, 0.04)',
  border: '1px solid rgba(255, 255, 255, 0.08)',
  borderRadius: '4px',
  padding: '1px 6px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
});

const ConditionsRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  flexWrap: 'wrap',
  marginTop: '$sm',
  paddingTop: '$xs',
  borderTop: '1px dashed $stroke',
});

const ConditionDot = styled(Flex, {
  alignItems: 'center',
  gap: '$xxxs',
  fontSize: '10px',
  color: '$body',

  variants: {
    state: {
      ok: { color: '$obolGreen' },
      pending: { color: '$obolGold' },
      missing: { color: '$body', opacity: 0.5 },
    },
  },
});

const Dot = styled(Box, {
  width: 6,
  height: 6,
  borderRadius: '$round',
  backgroundColor: 'currentColor',
});

const DrainingBadge = styled(Text, {
  fontSize: '10px',
  fontWeight: '$semibold',
  color: '#f5c542',
  backgroundColor: 'rgba(245, 197, 66, 0.1)',
  border: '1px solid rgba(245, 197, 66, 0.35)',
  borderRadius: '4px',
  padding: '1px 5px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
  fontVariantNumeric: 'tabular-nums',
});

const UsageBadge = styled(Text, {
  fontSize: '10px',
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

const ActionError = styled(Text, {
  marginTop: '$xxs',
  fontSize: '$1',
  color: '$test',
  textAlign: 'right',
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

const CONDITION_ORDER: Array<{ key: string; short: string }> = [
  { key: 'ModelReady', short: 'Model' },
  { key: 'UpstreamHealthy', short: 'Upstream' },
  { key: 'PaymentGateReady', short: 'Gate' },
  { key: 'RoutePublished', short: 'Route' },
  { key: 'Registered', short: 'Registered' },
];

function truncateAddress(address: string): string {
  if (address.length <= 10) return address;
  return `${address.slice(0, 6)}...${address.slice(-4)}`;
}

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

function conditionState(
  conditions: ServiceOfferCondition[] | undefined,
  type: string
): 'ok' | 'pending' | 'missing' {
  const c = conditions?.find(x => x.type === type);
  if (!c) return 'missing';
  if (c.status === 'True') return 'ok';
  return 'pending';
}

function conditionsToShow(offer: ServiceOffer) {
  return CONDITION_ORDER.filter(({ key }) => {
    if (key === 'ModelReady' && offer.type === 'http') return false;
    if (key === 'Registered' && !offer.registration?.enabled) return false;
    return true;
  });
}

function agentDisplayName(link: ServiceOfferAgentLink): string {
  if (link.runtime === 'hermes' && link.id === 'obol-agent')
    return 'Obol Agent';
  if (link.runtime === 'openclaw' && link.id === 'default') return 'OpenClaw';
  return link.id;
}

function scrollToAgent(link: ServiceOfferAgentLink) {
  const el = document.getElementById(`agent-${link.runtime}-${link.id}`);
  if (el) {
    el.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }
}

/**
 * Rewrite the controller-published endpoint (which uses the local
 * `obol.stack` hostname) to use the public Cloudflare tunnel as the base.
 * Returns null when the tunnel URL is unavailable so the local-only
 * `obol.stack` URL is never surfaced as a clickable link.
 *
 * The controller writes either a full URL (older offers) or a path-only
 * value like "/services/demo-quant" (current ServiceOffer status). We
 * resolve the latter against the tunnel URL as a base so both work.
 */
/**
 * Pulls the drain end time out of the Draining condition. Backend writes
 * the RFC3339 timestamp into condition.message; if that's ever missing we
 * fall back to deriving it from spec.drainAt + spec.drainGracePeriod (not
 * exposed on the typed ServiceOffer today, so we just return null and the
 * UI shows "Draining" with no countdown).
 */
function drainEndsAt(
  conditions: ServiceOfferCondition[] | undefined
): Date | null {
  const c = conditions?.find(x => x.type === 'Draining' && x.status === 'True');
  if (!c) return null;
  // Match the first ISO-8601-like substring in the message.
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

function publicEndpoint(
  endpoint: string | undefined,
  tunnelURL: string | null
): string | null {
  if (!tunnelURL) return null;
  try {
    const tunnel = new URL(tunnelURL);
    const tunnelPath = tunnel.pathname.replace(/\/$/, '');
    if (!endpoint) {
      return `${tunnel.origin}${tunnelPath}` || tunnel.toString();
    }
    const ep = new URL(endpoint, tunnel);
    return `${tunnel.origin}${tunnelPath}${ep.pathname}${ep.search}${ep.hash}`;
  } catch {
    return null;
  }
}

export function ServiceCard({ offer, tunnelURL, onChanged }: ServiceCardProps) {
  const [deleteState, setDeleteState] = useState<'idle' | 'submitting'>('idle');
  const [actionError, setActionError] = useState<string | null>(null);
  const displayName = offer.registration?.name || offer.name;
  const price = formatPrice(offer);
  const symbol = serviceOfferAssetSymbol(offer.payment);
  const network = offer.caip2Network || offer.payment?.network;
  const transferMethod = offer.payment?.asset?.transferMethod;
  // type=agent leaves spec.model and registration.skills empty by design;
  // the linked Agent's resolved view is the source of truth.
  const modelName = offer.model?.name || offer.agentResolution?.model;
  const skills =
    offer.registration?.skills && offer.registration.skills.length > 0
      ? offer.registration.skills
      : (offer.agentResolution?.skills ?? []);
  const trustBadges = serviceOfferTrustBadges(offer.registration);
  const visibleSkills = skills.slice(0, 3);
  const overflowSkills = Math.max(0, skills.length - visibleSkills.length);
  const charged = offer.usage?.chargedRequests;
  const endpoint = publicEndpoint(offer.endpoint, tunnelURL);

  const drainEnd =
    parseDrainEndsAt(offer.drainEndsAt) ?? drainEndsAt(offer.conditions);
  const isDraining = drainEnd !== null;
  const now = useNow(1000, isDraining);
  const countdown = drainEnd ? formatCountdown(drainEnd, now) : null;
  const isReady = !isDraining && offer.status === 'Ready';

  const handleDelete = async () => {
    const resource = `${offer.namespace}/${offer.name}`;
    const confirmed = window.confirm(
      `Delete ${resource}? This removes the ServiceOffer CR after controller cleanup.`
    );
    if (!confirmed) return;

    setDeleteState('submitting');
    setActionError(null);
    try {
      const response = await fetch(
        serviceOfferResourcePath(offer.name, offer.namespace),
        { method: 'DELETE' }
      );
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data?.error || `Delete failed (${response.status})`);
      }
      await onChanged?.();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'unknown error');
    } finally {
      setDeleteState('idle');
    }
  };

  return (
    <CardContainer>
      <TopRow>
        <Flex css={{ alignItems: 'center', gap: '$xs', flexWrap: 'wrap' }}>
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
          {offer.registrationPending && (
            <TypeBadge
              css={{
                color: '#f5c542',
                borderColor: 'rgba(245, 197, 66, 0.35)',
                background: 'rgba(245, 197, 66, 0.1)',
              }}
            >
              Registration pending
            </TypeBadge>
          )}
          {transferMethod && (
            <TypeBadge
              css={{
                color: '$body',
                borderColor: 'rgba(255, 255, 255, 0.12)',
              }}
            >
              {transferMethod}
            </TypeBadge>
          )}
          {endpoint && (
            <Link
              href={endpoint}
              target="_blank"
              title="Open service in new tab"
              css={{
                display: 'inline-flex',
                alignItems: 'center',
                fontSize: '13px',
              }}
            >
              <OpenInNew size="sm" />
            </Link>
          )}
        </Flex>
        <Flex css={{ alignItems: 'center', gap: '$xs' }}>
          {isDraining && (
            <DrainingBadge title={`Drain ends at ${drainEnd?.toISOString()}`}>
              Draining{countdown ? ` · ends in ${countdown}` : ''}
            </DrainingBadge>
          )}
          <StatusPill
            status={isDraining ? 'syncing' : isReady ? 'synced' : 'syncing'}
            label={isDraining ? 'Draining' : isReady ? 'Ready' : 'Pending'}
          />
        </Flex>
      </TopRow>

      {offer.registration?.description && (
        <Description>{offer.registration.description}</Description>
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
            {network && (
              <span style={{ marginLeft: '4px', color: 'inherit' }}>
                on {network}
              </span>
            )}
          </PriceValue>
        </PriceRow>
      )}

      <InfoRow>
        <InfoLabel>Fee recipient:</InfoLabel>
        {offer.agentLink ? (
          <AgentLinkText
            href={`#agent-${offer.agentLink.runtime}-${offer.agentLink.id}`}
            onClick={(e: MouseEvent<HTMLAnchorElement>) => {
              e.preventDefault();
              if (offer.agentLink) {
                scrollToAgent(offer.agentLink);
              }
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
        {charged !== null && charged !== undefined && (
          <UsageBadge>{pluralizeSales(charged)}</UsageBadge>
        )}
      </InfoRow>

      {endpoint && (
        <InfoRow>
          <InfoLabel>Endpoint:</InfoLabel>
          <InfoValue css={{ wordBreak: 'break-all' }}>{endpoint}</InfoValue>
          <CopyButton text={endpoint} title="Copy endpoint" />
        </InfoRow>
      )}

      {visibleSkills.length > 0 && (
        <SkillsRow>
          {visibleSkills.map(skill => (
            <SkillChip key={skill}>{skill}</SkillChip>
          ))}
          {overflowSkills > 0 && <SkillChip>+{overflowSkills}</SkillChip>}
        </SkillsRow>
      )}

      {trustBadges.length > 0 && (
        <SkillsRow>
          {trustBadges.map(method => (
            <SkillChip key={method} title="Supported trust method">
              Trust: {method}
            </SkillChip>
          ))}
        </SkillsRow>
      )}

      <ConditionsRow>
        {conditionsToShow(offer).map(({ key, short }) => {
          const state = conditionState(offer.conditions, key);
          return (
            <ConditionDot key={key} state={state}>
              <Dot />
              <Text css={{ fontSize: '10px', color: 'inherit' }}>{short}</Text>
            </ConditionDot>
          );
        })}
        <Text
          css={{
            marginLeft: 'auto',
            fontSize: '10px',
            color: '$body',
            opacity: 0.65,
            fontFamily: 'monospace',
          }}
        >
          {offer.namespace}/{offer.name}
        </Text>
      </ConditionsRow>

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
                title="Drain in-flight buyers, then tear down the route"
                disabled={deleteState === 'submitting'}
              >
                Stop &amp; drain
              </Button>
            }
          />
        )}
        <Button
          color="secondary"
          type="button"
          title="Delete the ServiceOffer CR"
          disabled={deleteState === 'submitting'}
          onClick={() => {
            void handleDelete();
          }}
          css={{
            color: '$test',
            borderColor: 'rgba(255, 107, 107, 0.35)',
          }}
        >
          {deleteState === 'submitting' ? 'Deleting...' : 'Delete'}
        </Button>
      </Flex>
      {actionError && <ActionError>{actionError}</ActionError>}
    </CardContainer>
  );
}
