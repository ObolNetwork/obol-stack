'use client';

import { useMemo, useState } from 'react';
import { Box, Text, styled } from '@obolnetwork/obol-ui';
import { useAccount } from 'wagmi';

import { Button } from '@/components/atoms/Button';
import { usePolledFetch } from '@/hooks/usePolledFetch';
import { useTunnelURL } from '@/hooks/useAgents';
import { tones } from '@/lib/ui/tones';
import type { ServiceOffer } from '@/types/sell';

import { ServicesSubNav } from './ServicesSubNav';
import { NewListingDialog } from './NewListingDialog';
import {
  Card,
  CountChip,
  Mono,
  SectionHeader,
  SectionTitle,
  TableHeaderRow,
} from './ServicesChrome';
import {
  EarningsStrip,
  formatAgo,
  toRollupBucket,
  type EarningsRollup,
} from './listings/EarningsStrip';
import { LISTING_TABLE_COLS, ListingRow } from './listings/ListingRow';

const Layout = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

// ── Empty state hint ───────────────────────────────────────────────────

const HintCard = styled('div', {
  marginTop: '$md',
  padding: '$md',
  background: '$bg02',
  border: '1px dashed $stroke',
  borderRadius: '$2',
  display: 'flex',
  alignItems: 'center',
  gap: '$md',
});

const HintIcon = styled('div', {
  width: 36,
  height: 36,
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  borderRadius: '$2',
  background: '$obolMidGreen',
  color: '$obolGreen',
  flexShrink: 0,
});

function ListMoreHint({ onOpen }: { onOpen: () => void }) {
  return (
    <HintCard>
      <HintIcon>
        <svg width="18" height="18" viewBox="0 0 16 16" fill="none">
          <path
            d="M8 3v10M3 8h10"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
          />
        </svg>
      </HintIcon>
      <Box css={{ flex: 1, fontSize: '$2', color: '$body', lineHeight: 1.5 }}>
        <Text css={{ color: '$light', fontWeight: '$semibold' }}>
          List another service.
        </Text>{' '}
        Expose any HTTP route or model behind x402. Run{' '}
        <Mono css={{ color: '$obolGreen' }}>obol sell inference</Mono> or{' '}
        <Mono css={{ color: '$obolGreen' }}>obol sell http</Mono> and we&apos;ll
        register it here.
      </Box>
      <Button type="button" variant="primary" onClick={onOpen}>
        New listing
      </Button>
    </HintCard>
  );
}

// ── Main page ──────────────────────────────────────────────────────────

interface SellListResp {
  offers?: ServiceOffer[];
  error?: string;
}

function offerKey(o: ServiceOffer): string {
  return `${o.namespace}/${o.name}`;
}

// Stable fallback so memos don't re-run while the first poll is in flight.
const NO_OFFERS: ServiceOffer[] = [];

export function MyListingsPage() {
  const { address } = useAccount();
  const { tunnelURL } = useTunnelURL();
  const [dialogOpen, setDialogOpen] = useState(false);

  const {
    data,
    loading,
    error: err,
    refetch: fetchOffers,
  } = usePolledFetch<ServiceOffer[]>('/api/sell/list', {
    mapResponse: body => (body as SellListResp).offers ?? NO_OFFERS,
    errorFallback: 'Failed to load listings',
  });
  const offers = data ?? NO_OFFERS;

  const myOffers = offers;

  const counts = useMemo(
    () => ({ listings: myOffers.length, browse: offers.length }),
    [myOffers, offers]
  );

  const rollup = useMemo<EarningsRollup>(() => {
    const totals = {
      base: { USDC: 0, OBOL: 0 },
      sepolia: { USDC: 0, OBOL: 0 },
      mainnet: { USDC: 0, OBOL: 0 },
    };
    let lastSettlementUnix = 0;

    for (const o of myOffers) {
      const priceStr = o.payment?.price?.perRequest ?? '0';
      const price = Number(priceStr);
      if (!Number.isFinite(price) || price <= 0) continue;
      const asset = (o.payment?.asset?.symbol ?? 'USDC').toUpperCase();
      if (asset !== 'USDC' && asset !== 'OBOL') continue;

      for (const c of o.usage?.chargedByChain ?? []) {
        const bucket = toRollupBucket(c.chain);
        if (!bucket) continue;
        totals[bucket][asset] += c.requests * price;
      }

      const t = o.usage?.lastSettlementUnix ?? 0;
      if (t > lastSettlementUnix) lastSettlementUnix = t;
    }

    const fmt = (n: number) => (n === 0 ? '0' : n.toFixed(4));
    const sumAcrossChains = (asset: 'USDC' | 'OBOL') =>
      totals.base[asset] + totals.sepolia[asset] + totals.mainnet[asset];

    return {
      total7d: {
        USDC: fmt(sumAcrossChains('USDC')),
        OBOL: fmt(sumAcrossChains('OBOL')),
      },
      perChain: {
        base: { USDC: fmt(totals.base.USDC), OBOL: fmt(totals.base.OBOL) },
        sepolia: {
          USDC: fmt(totals.sepolia.USDC),
          OBOL: fmt(totals.sepolia.OBOL),
        },
        mainnet: {
          USDC: fmt(totals.mainnet.USDC),
          OBOL: fmt(totals.mainnet.OBOL),
        },
      },
      lastSettlement:
        lastSettlementUnix > 0
          ? formatAgo(new Date(lastSettlementUnix * 1000))
          : '—',
    };
  }, [myOffers]);

  return (
    <Layout>
      <ServicesSubNav
        active="listings"
        counts={counts}
        onNewListing={() => setDialogOpen(true)}
      />

      <EarningsStrip rollup={rollup} />

      <Card>
        <SectionHeader>
          <SectionTitle>Active listings</SectionTitle>
          <CountChip>{myOffers.length}</CountChip>
          <Box css={{ flex: 1 }} />
          <Text css={{ fontSize: '$2', color: '$body' }}>
            7d ·{' '}
            <Mono css={{ color: '$obolGreen' }}>
              {rollup.total7d.USDC} USDC
            </Mono>{' '}
            earned
          </Text>
          <Button type="button" variant="ghost" size="sm" pill>
            Filter
          </Button>
        </SectionHeader>

        {offers.length > 0 && (
          <Text css={{ fontSize: '$1', color: '$muted', marginBottom: '$xs' }}>
            Showing all {offers.length} listing{offers.length === 1 ? '' : 's'}{' '}
            on this stack
            {address ? ` · connected wallet: ${address.slice(0, 8)}…` : ''}.
          </Text>
        )}

        {err ? (
          <Text css={{ fontSize: '$2', color: tones.danger.text }}>{err}</Text>
        ) : loading ? (
          <Text css={{ fontSize: '$2', color: '$muted' }}>
            Loading listings…
          </Text>
        ) : myOffers.length === 0 ? (
          <ListMoreHint onOpen={() => setDialogOpen(true)} />
        ) : (
          <>
            <TableHeaderRow css={{ gridTemplateColumns: LISTING_TABLE_COLS }}>
              <span>Listing</span>
              <span>Chain</span>
              <span style={{ textAlign: 'right' }}>Price</span>
              <span>Traffic · 24h</span>
              <span>Status</span>
              <span style={{ textAlign: 'right' }}>Actions</span>
            </TableHeaderRow>
            <Box css={{ display: 'flex', flexDirection: 'column', gap: '$xs' }}>
              {myOffers.map(offer => {
                const key = offerKey(offer);
                return (
                  <ListingRow
                    key={key}
                    offer={offer}
                    tunnelURL={tunnelURL}
                    mutators={{
                      onChanged: fetchOffers,
                    }}
                  />
                );
              })}
            </Box>
            <ListMoreHint onOpen={() => setDialogOpen(true)} />
          </>
        )}
      </Card>

      <NewListingDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
      />
    </Layout>
  );
}
