'use client';

import type { MouseEvent } from 'react';
import { Box, Flex, Text, Link, styled, OpenInNew } from '@obolnetwork/obol-ui';
import { StatusPill } from '../../atoms/StatusIndicator';
import { CopyButton } from '../../atoms/CopyButton/CopyButton';
import type {
  ServiceOffer,
  ServiceOfferAgentLink,
  ServiceOfferCondition,
  ServiceOfferType,
} from '@/types/sell';

interface ServiceCardProps {
  offer: ServiceOffer;
  tunnelURL: string | null;
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
 */
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
    const ep = new URL(endpoint);
    return `${tunnel.origin}${tunnelPath}${ep.pathname}${ep.search}${ep.hash}`;
  } catch {
    return null;
  }
}

export function ServiceCard({ offer, tunnelURL }: ServiceCardProps) {
  const displayName = offer.registration?.name || offer.name;
  const isReady = offer.status === 'Ready';
  const price = formatPrice(offer);
  const symbol = offer.payment?.asset?.symbol || 'USDC';
  const network = offer.payment?.network;
  const skills = offer.registration?.skills ?? [];
  const visibleSkills = skills.slice(0, 3);
  const overflowSkills = Math.max(0, skills.length - visibleSkills.length);
  const charged = offer.usage?.chargedRequests;
  const endpoint = publicEndpoint(offer.endpoint, tunnelURL);

  return (
    <CardContainer>
      <TopRow>
        <Flex css={{ alignItems: 'center', gap: '$xs', flexWrap: 'wrap' }}>
          <ServiceName>{displayName}</ServiceName>
          <TypeBadge>{TYPE_LABEL[offer.type]}</TypeBadge>
          {offer.model?.name && (
            <TypeBadge
              css={{
                color: '$obolGreen',
                borderColor: 'rgba(47, 228, 171, 0.3)',
                background: 'rgba(47, 228, 171, 0.08)',
              }}
            >
              {offer.model.name}
            </TypeBadge>
          )}
        </Flex>
        <StatusPill
          status={isReady ? 'synced' : 'syncing'}
          label={isReady ? 'Ready' : 'Pending'}
        />
      </TopRow>

      {offer.registration?.description && (
        <Description>{offer.registration.description}</Description>
      )}

      {price && (
        <PriceRow>
          <InfoLabel>Price:</InfoLabel>
          <PriceValue>
            {price.count} {symbol} / {price.unit}
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
          <Link href={endpoint} target="_blank" css={{ fontSize: '13px' }}>
            <OpenInNew size="sm" />
          </Link>
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
    </CardContainer>
  );
}
