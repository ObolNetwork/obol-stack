'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

import { Button } from '@/components/atoms/Button';
import { ChainIcon } from '@/components/atoms/ChainIcon';
import { CopyButton } from '@/components/atoms/CopyButton';
import { StopDrainModal } from '@/components/molecules/StopDrainModal';
import { toChainName } from '@/lib/marketplace/chain';
import { publicServiceEndpoint } from '@/lib/services/public-storefront';
import { tones, type ToneName } from '@/lib/ui/tones';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import type { ServiceOffer } from '@/types/sell';

import { Identicon } from '../Identicon';
import { Mono, RowBody } from '../ServicesChrome';
import { StatusPill } from '../StatusPill';

export const LISTING_TABLE_COLS =
  'minmax(260px, 1.6fr) 130px 130px minmax(180px, 1.1fr) 130px 130px';

const RowCard = styled('div', {
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  overflow: 'hidden',

  variants: {
    stuck: {
      paused: {
        borderColor: tones.warning.border,
        boxShadow: `inset 3px 0 0 0 ${tones.warning.text}`,
      },
      draining: {
        borderColor: tones.warning.border,
        boxShadow: `inset 3px 0 0 0 ${tones.warning.text}`,
      },
      unreachable: {
        borderColor: tones.danger.border,
        boxShadow: `inset 3px 0 0 0 ${tones.danger.text}`,
      },
    },
  },
});

const StuckStrip = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: 10,
  padding: '8px $md',
  fontSize: '$2',
  color: '$body',
  borderBottom: '1px solid',

  variants: {
    tone: {
      paused: {
        background: tones.warning.bg,
        borderColor: tones.warning.border,
      },
      draining: {
        background: tones.warning.bg,
        borderColor: tones.warning.border,
      },
      unreachable: {
        background: tones.danger.bg,
        borderColor: tones.danger.border,
      },
    },
  },
});

const ServiceCell = styled(Flex, {
  alignItems: 'center',
  gap: 12,
  minWidth: 0,
});

const BadgePill = styled('span', {
  padding: '2px 6px',
  borderRadius: '$1',
  fontSize: '$1',
  fontWeight: '$semibold',
  border: '1px solid $stroke',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  whiteSpace: 'nowrap',

  variants: {
    tone: {
      neutral: { background: '$bg03', color: '$body' },
      // Protocol/type chip keeps its own indigo hue — not a status tint.
      code: {
        background: 'rgba(98, 126, 234, 0.15)',
        color: '#8fa3f0',
        borderColor: 'rgba(98, 126, 234, 0.35)',
      },
      green: {
        background: tones.success.bg,
        color: tones.success.text,
        borderColor: tones.success.border,
      },
    },
  },
});

export type ListingStatus =
  | 'live'
  | 'paused'
  | 'draining'
  | 'unreachable'
  | 'degraded';

const STATUS_TONE: Record<ListingStatus, ToneName> = {
  live: 'success',
  paused: 'warning',
  draining: 'warning',
  unreachable: 'danger',
  degraded: 'neutral',
};

function hasDrainSignal(offer: ServiceOffer): boolean {
  if (offer.drainEndsAt) return true;
  return (
    offer.conditions?.some(
      condition => condition.type === 'Draining' && condition.status === 'True'
    ) ?? false
  );
}

export function deriveStatus(offer: ServiceOffer): ListingStatus {
  if (hasDrainSignal(offer)) return 'draining';
  if (offer.paused) return 'paused';
  const conditions = offer.conditions ?? [];
  const upstream = conditions.find(c => c.type === 'UpstreamHealthy');
  if (upstream && upstream.status !== 'True') return 'unreachable';
  const ready = conditions.find(c => c.type === 'Ready');
  if (ready) return ready.status === 'True' ? 'live' : 'degraded';
  return offer.status === 'Ready' ? 'live' : 'degraded';
}

function listingPrice(offer: ServiceOffer): string {
  return (
    offer.priceRaw ??
    offer.payment?.price?.perRequest ??
    offer.payment?.price?.perMTok ??
    offer.payment?.price?.perHour ??
    offer.payment?.price?.perEpoch ??
    '—'
  );
}

export interface RowMutators {
  onChanged: () => void | Promise<void>;
}

export interface ListingRowProps {
  offer: ServiceOffer;
  tunnelURL: string | null;
  mutators: RowMutators;
}

export function ListingRow({ offer, tunnelURL, mutators }: ListingRowProps) {
  const status = deriveStatus(offer);
  const stuck =
    status === 'draining'
      ? 'draining'
      : status === 'paused'
        ? 'paused'
        : status === 'unreachable'
          ? 'unreachable'
          : undefined;

  const chain = offer.caip2Network ?? offer.payment?.network ?? '';
  const chainIcon = toChainName(chain);
  const price = listingPrice(offer);
  const asset = offer.payment?.asset?.symbol ?? 'token';
  const registered = Boolean(
    offer.agentResolution || offer.endpoint || offer.registrationPending
  );
  const reqs = offer.usage?.requests24h ?? offer.usage?.chargedRequests ?? 0;
  const publicUrl =
    publicServiceEndpoint(offer.endpoint, tunnelURL) ??
    `http://obol.stack:8080/services/${offer.name}`;
  const registerCli = `obol sell register --chain ${offer.payment?.network ?? 'base-sepolia'}`;

  return (
    <RowCard stuck={stuck}>
      {stuck === 'unreachable' ? (
        <StuckStrip tone="unreachable">
          <Box css={{ flex: 1 }}>
            Buyers can&apos;t reach this listing — check{' '}
            <Mono css={{ color: '$obolGreen' }}>
              obol sell status {offer.name} -n {offer.namespace}
            </Mono>
          </Box>
        </StuckStrip>
      ) : stuck === 'draining' ? (
        <StuckStrip tone="draining">
          <Box css={{ flex: 1 }}>
            Coming off sale. Existing buyers can finish in-flight payments;
            public discovery shows the wind-down state.
          </Box>
        </StuckStrip>
      ) : null}
      <RowBody css={{ gridTemplateColumns: LISTING_TABLE_COLS }}>
        <ServiceCell>
          <Identicon seed={offer.name} size={32} />
          <Box css={{ minWidth: 0 }}>
            <Flex css={{ alignItems: 'center', gap: 8 }}>
              <Mono
                css={{
                  color: '$light',
                  fontSize: '$3',
                  fontWeight: '$semibold',
                }}
              >
                {offer.name}
              </Mono>
              <BadgePill tone="code">{offer.type.toUpperCase()}</BadgePill>
              {registered ? <BadgePill tone="green">ERC-8004</BadgePill> : null}
            </Flex>
            <Flex css={{ alignItems: 'center', gap: 6, marginTop: 3 }}>
              <Mono
                css={{
                  fontSize: '$1',
                  color: '$body',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                }}
              >
                {publicUrl}
              </Mono>
              <CopyButton text={publicUrl} title="Copy public URL" />
            </Flex>
          </Box>
        </ServiceCell>

        <Flex css={{ gap: 6, alignItems: 'center' }}>
          {chainIcon ? <ChainIcon chain={chainIcon} size={14} /> : null}
          <Text css={{ fontSize: '$2', color: '$body' }}>{chain || '—'}</Text>
        </Flex>

        <Box css={{ textAlign: 'right' }}>
          <Mono
            css={{ color: '$light', fontSize: '$3', fontWeight: '$semibold' }}
          >
            <ObolAmountSwitch amount={price} symbol={asset} align="right" />
          </Mono>
        </Box>

        <Box>
          <Mono
            css={{ color: '$light', fontSize: '$3', fontWeight: '$semibold' }}
          >
            {reqs.toLocaleString()}
          </Mono>{' '}
          <Text css={{ color: '$muted', fontSize: '$1', display: 'inline' }}>
            req · 24h
          </Text>
        </Box>

        <Box>
          <StatusPill tone={STATUS_TONE[status]} label={status} />
        </Box>

        <Flex css={{ justifyContent: 'flex-end', gap: 6, flexWrap: 'wrap' }}>
          {!registered ? (
            <CopyButton
              text={registerCli}
              title="Copy obol sell register command"
            />
          ) : null}
          {status !== 'draining' && (
            <StopDrainModal
              name={offer.name}
              namespace={offer.namespace}
              onDrained={() => {
                void mutators.onChanged();
              }}
              trigger={
                <Button
                  type="button"
                  size="sm"
                  title="Take this service off your storefront; in-flight buyers get a grace window"
                >
                  Stop selling
                </Button>
              }
            />
          )}
        </Flex>
      </RowBody>
    </RowCard>
  );
}
