'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { useAccount } from 'wagmi';

import { ChainIcon } from '@/components/atoms/ChainIcon';
import { CopyButton } from '@/components/atoms/CopyButton';
import { Identicon } from './Identicon';
import { ServicesSubNav } from './ServicesSubNav';
import { NewListingDialog } from './NewListingDialog';
import { toChainName } from '@/lib/marketplace/chain';
import { useOperatorWallets } from '@/hooks/useOperatorWallets';
import type { ServiceOffer } from '@/types/sell';

const Layout = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

const Card = styled(Box, {
  background: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$2',
  padding: '$md',
});

const SectionHeader = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  marginBottom: '$md',
});

const SectionTitle = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
});

const CountChip = styled('span', {
  background: '$bg03',
  border: '1px solid $stroke',
  padding: '2px 8px',
  borderRadius: '$pill',
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
});

const FilterBtn = styled('button', {
  height: 28,
  padding: '0 $xs',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  background: 'transparent',
  color: '$body',
  fontSize: '$1',
  cursor: 'pointer',
  '&:hover': { background: '$bg03', color: '$light' },
});

// ── EarningsStrip ──────────────────────────────────────────────────────

const StripGrid = styled('div', {
  display: 'grid',
  gridTemplateColumns: 'minmax(220px, 1.1fr) repeat(3, 1fr) auto',
  alignItems: 'stretch',
});

const StripCell = styled(Box, {
  padding: '0 $md',
  display: 'flex',
  flexDirection: 'column',
  gap: 8,
  borderRight: '1px solid $stroke',

  variants: {
    last: { true: { borderRight: 'none' } },
    first: { true: { paddingLeft: 0, paddingRight: '$md' } },
  },
});

const FieldLabel = styled('div', {
  fontSize: '$1',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$muted',
  fontWeight: '$semibold',
});

const BigUSDC = styled('span', {
  fontFamily: 'monospace',
  fontSize: '$7',
  fontWeight: '$semibold',
  color: '$obolGreen',
  letterSpacing: '-0.02em',
});

const Mono = styled('span', {
  fontFamily: 'monospace',
});

const Sub = styled('div', {
  fontSize: '$1',
  color: '$body',
});

interface EarningsRollup {
  total7d: { USDC: string; OBOL: string };
  perChain: Record<
    'base' | 'sepolia' | 'mainnet',
    { USDC: string; OBOL: string }
  >;
  lastSettlement: string;
}

function toRollupBucket(chain: string): 'base' | 'sepolia' | 'mainnet' | null {
  const c = chain.toLowerCase();
  if (c === 'base' || c === 'eip155:8453') return 'base';
  if (c === 'base-sepolia' || c === 'sepolia' || c === 'eip155:84532')
    return 'sepolia';
  if (c === 'mainnet' || c === 'ethereum' || c === 'eip155:1') return 'mainnet';
  return null;
}

function formatAgo(d: Date): string {
  const seconds = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

function EarningsStrip({ rollup }: { rollup: EarningsRollup }) {
  return (
    <Card css={{ padding: '$md' }}>
      <StripGrid>
        <StripCell first>
          <FieldLabel>Earned · last 7d</FieldLabel>
          <Flex css={{ alignItems: 'baseline', gap: 6 }}>
            <BigUSDC>{rollup.total7d.USDC}</BigUSDC>
            <Text css={{ fontSize: '$2', color: '$body' }}>USDC</Text>
          </Flex>
          <Flex css={{ alignItems: 'baseline', gap: 6 }}>
            <Mono css={{ fontSize: '$3', color: '$body' }}>
              + {rollup.total7d.OBOL}
            </Mono>
            <Text css={{ fontSize: '$1', color: '$muted' }}>OBOL</Text>
          </Flex>
        </StripCell>

        {(['base', 'sepolia', 'mainnet'] as const).map((c, i) => {
          const { USDC, OBOL } = rollup.perChain[c];
          const empty = parseFloat(USDC) === 0 && parseFloat(OBOL) === 0;
          const label = c === 'sepolia' ? 'base-sepolia' : c;
          return (
            <StripCell key={c} css={empty ? { opacity: 0.55 } : {}}>
              <Flex css={{ gap: 6, alignItems: 'center' }}>
                <ChainIcon chain={label} size={14} />
                <Text css={{ fontSize: '$1', color: '$body' }}>
                  {c === 'sepolia'
                    ? 'Base Sepolia'
                    : c === 'base'
                      ? 'Base'
                      : 'Mainnet'}
                </Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono
                  css={{
                    fontSize: '$5',
                    color: empty ? '$muted' : '$light',
                    fontWeight: '$semibold',
                  }}
                >
                  {USDC}
                </Mono>
                <Text css={{ fontSize: '$1', color: '$body' }}>USDC</Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono
                  css={{ fontSize: '$3', color: empty ? '$muted' : '$body' }}
                >
                  {OBOL}
                </Mono>
                <Text css={{ fontSize: '$1', color: '$muted' }}>OBOL</Text>
              </Flex>
            </StripCell>
          );
        })}

        <StripCell
          last
          css={{
            borderLeft: '1px solid $stroke',
            paddingLeft: '$md',
            justifyContent: 'space-between',
          }}
        >
          <FieldLabel>Last settlement</FieldLabel>
          <Mono css={{ fontSize: '$2', color: '$light' }}>
            {rollup.lastSettlement}
          </Mono>
        </StripCell>
      </StripGrid>
    </Card>
  );
}

// ── Listings table ─────────────────────────────────────────────────────

const TABLE_COLS =
  'minmax(260px, 1.6fr) 130px 130px minmax(180px, 1.1fr) 130px 130px';

const TableHeaderRow = styled('div', {
  display: 'grid',
  gridTemplateColumns: TABLE_COLS,
  gap: '$sm',
  padding: '0 $md 8px',
  fontSize: '$1',
  color: '$body',
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
  fontWeight: '$semibold',
});

const RowCard = styled('div', {
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  overflow: 'hidden',

  variants: {
    stuck: {
      paused: {
        borderColor: 'rgba(250, 186, 90, 0.45)',
        boxShadow: 'inset 3px 0 0 0 #FABA5A',
      },
      unreachable: {
        borderColor: 'rgba(221, 96, 60, 0.55)',
        boxShadow: 'inset 3px 0 0 0 #DD603C',
      },
    },
  },
});

const RowBody = styled('div', {
  padding: '$sm $md',
  display: 'grid',
  gridTemplateColumns: TABLE_COLS,
  gap: '$sm',
  alignItems: 'center',
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
        background: 'rgba(250, 186, 90, 0.07)',
        borderColor: 'rgba(250, 186, 90, 0.25)',
      },
      unreachable: {
        background: 'rgba(221, 96, 60, 0.08)',
        borderColor: 'rgba(221, 96, 60, 0.25)',
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
      code: {
        background: 'rgba(98, 126, 234, 0.15)',
        color: '#8fa3f0',
        borderColor: 'rgba(98, 126, 234, 0.35)',
      },
      green: {
        background: '$obolMidGreen',
        color: '$obolGreen',
        borderColor: '$obolGreen',
      },
    },
  },
});

const StatusPill = styled('span', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 6,
  padding: '2px 8px',
  borderRadius: '$pill',
  fontSize: '$1',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  border: '1px solid',

  variants: {
    status: {
      live: {
        background: '$obolMidGreen',
        color: '$obolGreen',
        borderColor: '$obolGreen',
      },
      paused: {
        background: 'rgba(250, 186, 90, 0.10)',
        color: '#FABA5A',
        borderColor: 'rgba(250, 186, 90, 0.5)',
      },
      unreachable: {
        background: 'rgba(221, 96, 60, 0.10)',
        color: '#DD603C',
        borderColor: 'rgba(221, 96, 60, 0.5)',
      },
      degraded: { background: '$bg03', color: '$body', borderColor: '$stroke' },
    },
  },
});

const RowAction = styled('button', {
  height: 28,
  padding: '0 12px',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  background: '$bg03',
  color: '$light',
  fontSize: '$1',
  cursor: 'pointer',
  '&:hover': { background: '$bg04' },

  variants: {
    tone: {
      primary: {
        background: '$obolGreen',
        color: '$obolDarkDark',
        borderColor: '$obolGreen',
        fontWeight: '$semibold',
        '&:hover': { filter: 'brightness(1.05)' },
      },
    },
  },
});

type ListingStatus = 'live' | 'paused' | 'unreachable' | 'degraded';

function deriveStatus(offer: ServiceOffer): ListingStatus {
  if (offer.paused) return 'paused';
  const conditions = offer.conditions ?? [];
  const upstream = conditions.find(c => c.type === 'UpstreamHealthy');
  if (upstream && upstream.status !== 'True') return 'unreachable';
  const ready = conditions.find(c => c.type === 'Ready');
  if (ready?.status !== 'True') return 'degraded';
  return 'live';
}

interface RowMutators {
  onPauseToggle: (offer: ServiceOffer) => void;
  onDelete: (offer: ServiceOffer) => void;
  pending: boolean;
}

function ListingRow({
  offer,
  mutators,
}: {
  offer: ServiceOffer;
  mutators: RowMutators;
}) {
  const status = deriveStatus(offer);
  const stuck =
    status === 'paused'
      ? 'paused'
      : status === 'unreachable'
        ? 'unreachable'
        : undefined;

  const chain = offer.payment?.network ?? '';
  const chainIcon = toChainName(chain);
  const price = offer.payment?.price?.perRequest ?? '—';
  const asset = offer.payment?.asset?.symbol ?? 'USDC';
  const registered = Boolean(offer.agentResolution || offer.endpoint);
  const reqs = offer.usage?.requests24h ?? offer.usage?.chargedRequests ?? 0;

  return (
    <RowCard stuck={stuck}>
      {stuck === 'unreachable' ? (
        <StuckStrip tone="unreachable">
          <Box css={{ flex: 1 }}>Buyers can&apos;t reach this listing.</Box>
          <FilterBtn>Logs</FilterBtn>
        </StuckStrip>
      ) : null}
      <RowBody>
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
                {offer.endpoint ?? `/services/${offer.name}/*`}
              </Mono>
              <CopyButton
                text={offer.endpoint ?? `/services/${offer.name}/*`}
              />
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
            {price}
          </Mono>{' '}
          <Text css={{ color: '$body', fontSize: '$2', display: 'inline' }}>
            {asset}
          </Text>
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
          <StatusPill status={status}>{status}</StatusPill>
        </Box>

        <Flex css={{ justifyContent: 'flex-end', gap: 6 }}>
          {status === 'paused' ? (
            <RowAction
              tone="primary"
              disabled={mutators.pending}
              onClick={() => mutators.onPauseToggle(offer)}
            >
              {mutators.pending ? '…' : 'Resume'}
            </RowAction>
          ) : status === 'unreachable' ? (
            <RowAction
              tone="primary"
              disabled={mutators.pending}
              onClick={() => mutators.onPauseToggle(offer)}
            >
              {mutators.pending ? '…' : 'Pause'}
            </RowAction>
          ) : (
            <RowAction
              disabled={mutators.pending}
              onClick={() => mutators.onPauseToggle(offer)}
            >
              {mutators.pending ? '…' : 'Pause'}
            </RowAction>
          )}
          <RowAction
            disabled={mutators.pending}
            onClick={() => mutators.onDelete(offer)}
            title="Delete listing"
          >
            ×
          </RowAction>
        </Flex>
      </RowBody>
    </RowCard>
  );
}

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
      <RowAction tone="primary" onClick={onOpen}>
        New listing
      </RowAction>
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

export function MyListingsPage() {
  const { address } = useAccount();
  const operator = useOperatorWallets();
  const [offers, setOffers] = useState<ServiceOffer[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set());

  const fetchOffers = useCallback(async () => {
    setErr(null);
    try {
      const r = await fetch('/api/sell/list', { cache: 'no-store' });
      const body: SellListResp = await r.json();
      if (!r.ok) throw new Error(body.error ?? `HTTP ${r.status}`);
      setOffers(body.offers ?? []);
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed to load listings');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchOffers();
    const id = setInterval(fetchOffers, 30_000);
    return () => clearInterval(id);
  }, [fetchOffers]);

  const markPending = useCallback((key: string, on: boolean) => {
    setPending(prev => {
      const next = new Set(prev);
      if (on) next.add(key);
      else next.delete(key);
      return next;
    });
  }, []);

  const handlePauseToggle = useCallback(
    async (offer: ServiceOffer) => {
      const key = offerKey(offer);
      const action = offer.paused ? 'resume' : 'pause';
      markPending(key, true);
      try {
        const r = await fetch(
          `/api/sell/${encodeURIComponent(offer.name)}?ns=${encodeURIComponent(offer.namespace)}&action=${action}`,
          { method: 'PATCH' }
        );
        if (!r.ok) {
          const body = await r.json().catch(() => ({}));
          throw new Error(body.error ?? `HTTP ${r.status}`);
        }
        await fetchOffers();
      } catch (e) {
        setErr(
          e instanceof Error ? e.message : `Failed to ${action} ${offer.name}`
        );
      } finally {
        markPending(key, false);
      }
    },
    [fetchOffers, markPending]
  );

  const handleDelete = useCallback(
    async (offer: ServiceOffer) => {
      const confirmed =
        typeof window === 'undefined'
          ? false
          : window.confirm(
              `Delete listing "${offer.name}" in ${offer.namespace}? This removes the HTTPRoute, Middleware, and ERC-8004 registration request. Cannot be undone.`
            );
      if (!confirmed) return;
      const key = offerKey(offer);
      markPending(key, true);
      try {
        const r = await fetch(
          `/api/sell/${encodeURIComponent(offer.name)}?ns=${encodeURIComponent(offer.namespace)}`,
          { method: 'DELETE' }
        );
        if (!r.ok) {
          const body = await r.json().catch(() => ({}));
          throw new Error(body.error ?? `HTTP ${r.status}`);
        }
        await fetchOffers();
      } catch (e) {
        setErr(
          e instanceof Error ? e.message : `Failed to delete ${offer.name}`
        );
      } finally {
        markPending(key, false);
      }
    },
    [fetchOffers, markPending]
  );

  const myOffers = useMemo(() => {
    // Cold start: while we still don't know the operator's wallet set,
    // show everything so the table isn't blank during fetch.
    if (operator.isLoading && operator.addressSet.size === 0) return offers;
    if (operator.addressSet.size === 0) return offers;
    return offers.filter(o => {
      const payTo = (o.payment?.payTo ?? '').toLowerCase();
      return payTo && operator.addressSet.has(payTo);
    });
  }, [offers, operator.addressSet, operator.isLoading]);

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
            today ·{' '}
            <Mono css={{ color: '$obolGreen' }}>
              {rollup.total7d.USDC} USDC
            </Mono>{' '}
            earned
          </Text>
          <FilterBtn type="button">Filter</FilterBtn>
        </SectionHeader>

        {operator.addressSet.size > 0 ? (
          <Text css={{ fontSize: '$1', color: '$muted', marginBottom: '$xs' }}>
            Showing listings paid to {operator.agents.length} agent wallet
            {operator.agents.length === 1 ? '' : 's'}
            {address ? ' + your connected wallet' : ''}.
          </Text>
        ) : (
          <Text css={{ fontSize: '$2', color: '$muted' }}>
            No agent wallets discovered yet and no browser wallet connected —
            showing all {offers.length} listing
            {offers.length === 1 ? '' : 's'} on this stack.
          </Text>
        )}

        {err ? (
          <Text css={{ fontSize: '$2', color: '#DD603C' }}>{err}</Text>
        ) : loading ? (
          <Text css={{ fontSize: '$2', color: '$muted' }}>
            Loading listings…
          </Text>
        ) : myOffers.length === 0 ? (
          <ListMoreHint onOpen={() => setDialogOpen(true)} />
        ) : (
          <>
            <TableHeaderRow>
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
                    mutators={{
                      onPauseToggle: handlePauseToggle,
                      onDelete: handleDelete,
                      pending: pending.has(key),
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
