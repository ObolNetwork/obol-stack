'use client';

import { useEffect, useMemo, useState } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { useAccount } from 'wagmi';

import { ChainIcon } from '@/components/atoms/ChainIcon';
import { CopyButton } from '@/components/atoms/CopyButton';
import { Identicon } from './Identicon';
import { ServicesSubNav } from './ServicesSubNav';
import { NewListingDialog } from './NewListingDialog';
import { toChainName } from '@/lib/marketplace/chain';
import {
  formatAge,
  formatSettlementTimestamp,
} from '@/lib/marketplace/settlement';
import { useWalletAssetBalances } from '@/hooks/useWalletAssetBalances';
import { useOperatorWallets } from '@/hooks/useOperatorWallets';
import type { PurchaseListResponse, PurchaseRequest } from '@/types/purchase';

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

const FieldLabel = styled('div', {
  fontSize: '$1',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$muted',
  fontWeight: '$semibold',
});

const Mono = styled('span', { fontFamily: 'monospace' });

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

// ── WalletStrip ────────────────────────────────────────────────────────

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

function shortAddr(addr: string, prefix = 6, suffix = 4): string {
  if (addr.length <= prefix + suffix + 2) return addr;
  return `${addr.slice(0, prefix)}…${addr.slice(-suffix)}`;
}

// Maps our short chain key to (chainId, label, ChainIcon name).
const STRIP_CHAINS: ReadonlyArray<{
  key: 'mainnet' | 'base' | 'base-sepolia';
  chainId: number;
  label: string;
  icon: 'mainnet' | 'base' | 'base-sepolia';
}> = [
  { key: 'base', chainId: 8453, label: 'Base', icon: 'base' },
  {
    key: 'base-sepolia',
    chainId: 84532,
    label: 'Base Sepolia',
    icon: 'base-sepolia',
  },
  { key: 'mainnet', chainId: 1, label: 'Mainnet', icon: 'mainnet' },
];

function WalletStrip({
  address,
  agentCount,
}: {
  address: string | undefined;
  agentCount: number;
}) {
  // useWalletAssetBalances handles its own polling (60s) + per-chain
  // multicall3 batching via eRPC. When address is empty, the hook short-circuits
  // and returns an empty array — safe to call unconditionally per Rules of Hooks.
  const { balances } = useWalletAssetBalances(address ?? null);
  const [lastFetched, setLastFetched] = useState<Date | null>(null);
  useEffect(() => {
    if (balances.length > 0) setLastFetched(new Date());
  }, [balances]);

  const findBalance = (chainId: number, symbol: 'USDC' | 'OBOL'): string => {
    const hit = balances.find(
      b => b.chain.chainId === chainId && b.token.symbol === symbol
    );
    return hit?.formatted ?? '0';
  };

  return (
    <Card css={{ padding: '$md' }}>
      <StripGrid>
        <StripCell first>
          <FieldLabel>
            Agent wallet{agentCount > 1 ? `s · ${agentCount}` : ''}
          </FieldLabel>
          {address ? (
            <Flex css={{ gap: 10, alignItems: 'center' }}>
              <Identicon seed={address} size={28} />
              <Mono
                css={{ fontSize: '$3', color: '$light', fontWeight: '$medium' }}
              >
                {shortAddr(address, 10, 8)}
              </Mono>
              <CopyButton text={address} />
            </Flex>
          ) : (
            <Text css={{ fontSize: '$2', color: '$muted' }}>
              {agentCount > 0
                ? 'Balances aggregate across agent wallets.'
                : 'No agent wallets discovered yet.'}
            </Text>
          )}
        </StripCell>

        {STRIP_CHAINS.map(c => {
          const usdc = findBalance(c.chainId, 'USDC');
          const obol = findBalance(c.chainId, 'OBOL');
          const empty = usdc === '0' && obol === '0';
          return (
            <StripCell key={c.key} css={empty ? { opacity: 0.55 } : {}}>
              <Flex css={{ gap: 6, alignItems: 'center' }}>
                <ChainIcon chain={c.icon} size={14} />
                <Text css={{ fontSize: '$1', color: '$body' }}>{c.label}</Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono
                  css={{
                    fontSize: '$5',
                    color: empty ? '$muted' : '$light',
                    fontWeight: '$semibold',
                  }}
                >
                  {usdc}
                </Mono>
                <Text css={{ fontSize: '$1', color: '$body' }}>USDC</Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono
                  css={{ fontSize: '$3', color: empty ? '$muted' : '$body' }}
                >
                  {obol}
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
          <FieldLabel>Last fetched</FieldLabel>
          <Mono css={{ fontSize: '$2', color: '$light' }}>
            {lastFetched ? formatRelative(lastFetched) : '—'}
          </Mono>
        </StripCell>
      </StripGrid>
    </Card>
  );
}

function formatRelative(d: Date): string {
  const seconds = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  return `${h}h ago`;
}

// ── Purchase table ─────────────────────────────────────────────────────

const TABLE_COLS =
  'minmax(220px, 1.4fr) 130px 110px minmax(220px, 1.4fr) 150px 100px';

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
      true: {
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
  background: 'rgba(221, 96, 60, 0.08)',
  borderBottom: '1px solid rgba(221, 96, 60, 0.25)',
});

const AuthBar = styled('div', {
  position: 'relative',
  height: 6,
  width: '100%',
  background: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$pill',
  overflow: 'hidden',
});

const AuthFill = styled('div', {
  position: 'absolute',
  top: 0,
  bottom: 0,
  left: 0,
  background: '$obolGreen',

  variants: {
    low: {
      true: { background: '#FABA5A' },
    },
    empty: {
      true: { background: '#DD603C' },
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
      on: {
        background: '$obolMidGreen',
        color: '$obolGreen',
        borderColor: '$obolGreen',
      },
      off: { background: '$bg03', color: '$body', borderColor: '$stroke' },
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
      },
    },
  },
});

// ── Expansion drawer ───────────────────────────────────────────────────

const Expansion = styled('div', {
  borderTop: '1px solid $stroke',
  padding: '$sm $md $md calc($md + 56px)',
  display: 'grid',
  gridTemplateColumns: '1.2fr 1fr',
  gap: '$sm',

  '@media (max-width: 900px)': {
    gridTemplateColumns: 'minmax(0, 1fr)',
    paddingLeft: '$md',
  },
});

const InnerPanel = styled('div', {
  background: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$2',
  padding: '$sm $sm',
  display: 'flex',
  flexDirection: 'column',
  gap: 8,
});

const RouteShell = styled(Flex, {
  alignItems: 'center',
  gap: 8,
  padding: '8px 10px',
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
});

/**
 * Renders the most-recent verifier-observed settlement for this purchase.
 * Sourced server-side from `obol_x402_verifier_last_payment_success_seconds`
 * — see /api/marketplace/purchases. Empty state ("no settlements yet") is
 * preserved when the offer has never been charged, when Prometheus is
 * unavailable, or when the endpoint is an external x402 seller with no
 * matching ServiceOffer in this cluster.
 */
function SettlementRow({
  lastSettlementUnix,
}: {
  lastSettlementUnix: number | null;
}) {
  // Re-render the human-readable age every 15s so "12s ago" doesn't go stale
  // while the drawer stays open. The underlying timestamp comes from the
  // purchases poll (every 30s) so the data freshness still tracks that.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!lastSettlementUnix) return;
    const id = setInterval(() => setNow(Date.now()), 15_000);
    return () => clearInterval(id);
  }, [lastSettlementUnix]);

  if (!lastSettlementUnix || lastSettlementUnix <= 0) {
    return (
      <Text css={{ fontSize: '$1', color: '$muted', lineHeight: 1.5 }}>
        No settlements yet.
      </Text>
    );
  }

  const iso = formatSettlementTimestamp(lastSettlementUnix);
  const ageSec = Math.max(0, Math.floor(now / 1000 - lastSettlementUnix));
  const age = formatAge(ageSec);

  return (
    <Text css={{ fontSize: '$1', color: '$muted', lineHeight: 1.5 }}>
      Last settled: <Mono css={{ color: '$light' }}>{iso}</Mono>{' '}
      <Text css={{ color: '$body', display: 'inline' }}>({age})</Text>
    </Text>
  );
}

function PurchaseExpansion({ p }: { p: PurchaseRequest }) {
  const route = p.status.publicModel
    ? `paid/${p.status.publicModel.replace(/^paid\//, '')}`
    : '—';
  const refill = p.spec.autoRefill;

  return (
    <Expansion>
      <InnerPanel>
        <FieldLabel>Settlement log</FieldLabel>
        <SettlementRow
          lastSettlementUnix={p.settlement?.lastSettlementUnix ?? null}
        />
        <Flex css={{ gap: 16, fontFamily: 'monospace', fontSize: '$2' }}>
          <Box>
            <FieldLabel>Total spent</FieldLabel>
            <Mono css={{ color: '$light' }}>{p.status.totalSpent ?? '0'}</Mono>
          </Box>
          <Box>
            <FieldLabel>Total signed</FieldLabel>
            <Mono css={{ color: '$light' }}>{p.status.totalSigned}</Mono>
          </Box>
          <Box>
            <FieldLabel>Wallet bal.</FieldLabel>
            <Mono css={{ color: '$light' }}>
              {p.status.walletBalance ?? '—'}
            </Mono>
          </Box>
        </Flex>
      </InnerPanel>

      <Flex css={{ flexDirection: 'column', gap: '$sm' }}>
        <InnerPanel>
          <FieldLabel>LiteLLM route</FieldLabel>
          <RouteShell>
            <Mono
              css={{
                flex: 1,
                fontSize: '$2',
                color: '$obolGreen',
                minWidth: 0,
              }}
            >
              {route}
            </Mono>
            <CopyButton text={route} />
          </RouteShell>
          <Text css={{ fontSize: '$1', color: '$muted', lineHeight: 1.5 }}>
            Call it like any model:{' '}
            <Mono css={{ color: '$body' }}>model=&quot;{route}&quot;</Mono>.
            Payments spend from the buyer sidecar pool.
          </Text>
        </InnerPanel>

        <InnerPanel>
          <FieldLabel>Auto-refill policy</FieldLabel>
          <Flex css={{ gap: 12, fontSize: '$2' }}>
            <Box css={{ flex: 1 }}>
              <Text css={{ fontSize: '$1', color: '$muted' }}>Threshold</Text>
              <Mono css={{ color: '$light' }}>
                {refill?.threshold ?? 0} auths left
              </Mono>
            </Box>
            <Box css={{ flex: 1 }}>
              <Text css={{ fontSize: '$1', color: '$muted' }}>
                Refill count
              </Text>
              <Mono css={{ color: '$light' }}>
                {refill?.count ?? 0} × per cycle
              </Mono>
            </Box>
            <Box css={{ flex: 1 }}>
              <Text css={{ fontSize: '$1', color: '$muted' }}>Daily cap</Text>
              <Mono css={{ color: '$light' }}>
                {refill?.maxSpendPerDay ?? '—'}
              </Mono>
            </Box>
          </Flex>
        </InnerPanel>
      </Flex>
    </Expansion>
  );
}

const ChevronBtn = styled('button', {
  width: 22,
  height: 22,
  padding: 0,
  background: 'transparent',
  border: 0,
  color: '$body',
  cursor: 'pointer',
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  borderRadius: 4,
  transition: 'transform 200ms, color 150ms',
  flexShrink: 0,
  '&:hover': { color: '$light' },

  variants: {
    open: {
      true: { transform: 'rotate(180deg)' },
    },
  },
});

function ChevronIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" fill="none">
      <path
        d="M4 6l4 4 4-4"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function PurchaseRow({
  p,
  expanded,
  onToggle,
}: {
  p: PurchaseRequest;
  expanded: boolean;
  onToggle: () => void;
}) {
  const remaining = p.status.remaining;
  const total = p.spec.count || p.status.totalSigned || 0;
  const refillOn = p.spec.autoRefill?.enabled ?? false;
  const chainIcon = toChainName(p.spec.payment.network);
  const stuck = remaining === 0 && !refillOn;
  const fillPct = total > 0 ? Math.min(100, (remaining / total) * 100) : 0;
  const threshold = p.spec.autoRefill?.threshold ?? 0;
  const low = total > 0 && remaining > 0 && remaining <= threshold;

  return (
    <RowCard stuck={stuck}>
      {stuck ? (
        <StuckStrip>
          <Box css={{ flex: 1 }}>
            Auth pool empty and refill is off. Top up or arm auto-refill to keep
            the route live.
          </Box>
          <FilterBtn>Logs</FilterBtn>
        </StuckStrip>
      ) : null}
      <RowBody>
        <Flex css={{ alignItems: 'center', gap: 8, minWidth: 0 }}>
          <ChevronBtn
            type="button"
            open={expanded}
            onClick={onToggle}
            aria-expanded={expanded}
            aria-label={expanded ? 'Collapse details' : 'Expand details'}
          >
            <ChevronIcon />
          </ChevronBtn>
          <Identicon seed={p.spec.payment.payTo || p.name} size={32} />
          <Box css={{ minWidth: 0 }}>
            <Mono
              css={{ color: '$light', fontSize: '$3', fontWeight: '$semibold' }}
            >
              {p.name}
            </Mono>
            <Text css={{ fontSize: '$1', color: '$body' }}>
              {p.spec.model ? (
                <>
                  model <Mono>{p.spec.model}</Mono>
                </>
              ) : (
                <Mono>{p.spec.endpoint}</Mono>
              )}
            </Text>
          </Box>
        </Flex>

        <Flex css={{ gap: 6, alignItems: 'center' }}>
          {chainIcon ? <ChainIcon chain={chainIcon} size={14} /> : null}
          <Text css={{ fontSize: '$2', color: '$body' }}>
            {p.spec.payment.network || '—'}
          </Text>
        </Flex>

        <Box css={{ textAlign: 'right' }}>
          <Mono
            css={{ color: '$light', fontSize: '$2', fontWeight: '$semibold' }}
          >
            {p.spec.payment.price}
          </Mono>{' '}
          <Text css={{ color: '$body', fontSize: '$1', display: 'inline' }}>
            {p.spec.payment.assetSymbol ?? p.spec.payment.asset.slice(0, 6)}
          </Text>
        </Box>

        <Box>
          {total > 0 ? (
            <Flex css={{ flexDirection: 'column', gap: 4 }}>
              <AuthBar>
                <AuthFill
                  low={low}
                  empty={remaining === 0}
                  css={{ width: `${fillPct}%` }}
                />
              </AuthBar>
              <Mono css={{ fontSize: '$1', color: '$body' }}>
                {remaining} / {total}{' '}
                {threshold ? (
                  <Text css={{ color: '$muted', display: 'inline' }}>
                    · refill at {threshold}
                  </Text>
                ) : null}
              </Mono>
            </Flex>
          ) : (
            <Text css={{ fontSize: '$2', color: '$muted' }}>—</Text>
          )}
        </Box>

        <Box>
          <StatusPill status={refillOn ? 'on' : 'off'}>
            {refillOn ? 'on' : 'off'}
          </StatusPill>
          <Text
            css={{
              fontSize: '$1',
              color: '$muted',
              fontFamily: 'monospace',
              marginTop: 4,
            }}
          >
            {refillOn
              ? `count ${p.spec.autoRefill?.count ?? 0}`
              : 'Not configured'}
          </Text>
        </Box>

        <Flex css={{ justifyContent: 'flex-end' }}>
          <RowAction tone={stuck ? 'primary' : undefined}>
            {stuck ? `Top up ${p.spec.autoRefill?.count ?? 50}` : 'Top up'}
          </RowAction>
        </Flex>
      </RowBody>
      {expanded ? <PurchaseExpansion p={p} /> : null}
    </RowCard>
  );
}

// ── Main page ──────────────────────────────────────────────────────────

export function MyPurchasesPage() {
  const { address } = useAccount();
  const operator = useOperatorWallets();
  const [purchases, setPurchases] = useState<PurchaseRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [expandedKey, setExpandedKey] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      setLoading(true);
      setErr(null);
      try {
        const r = await fetch('/api/marketplace/purchases', {
          cache: 'no-store',
        });
        const body = (await r.json()) as PurchaseListResponse & {
          error?: string;
        };
        if (cancelled) return;
        if (!r.ok) throw new Error(body.error ?? `HTTP ${r.status}`);
        setPurchases(body.purchases ?? []);
      } catch (e) {
        if (cancelled) return;
        setErr(e instanceof Error ? e.message : 'Failed to load purchases');
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    load();
    const id = setInterval(load, 30_000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  // Primary wallet for the WalletStrip header: prefer the first agent's
  // first address (most operators have a single obol-agent), falling back
  // to the browser-connected wallet so the strip still shows something
  // when no agents are discovered (purely-operator-driven stacks).
  const primaryWallet = useMemo<string | undefined>(() => {
    const first = operator.agents
      .flatMap(a => a.addresses)
      .find(addr => typeof addr === 'string' && addr.length > 0);
    return first ?? address;
  }, [operator.agents, address]);

  const myPurchases = useMemo(() => {
    // Until the operator-wallet set has been fetched at least once, show
    // everything so the table isn't empty during the cold-load round-trip.
    if (operator.isLoading && operator.addressSet.size === 0) return purchases;
    // If we never resolved any operator wallets (no agents listed, no
    // browser wallet connected), fall back to showing all purchases.
    if (operator.addressSet.size === 0) return purchases;
    return purchases.filter(p => {
      const signer = (p.status.signerAddress ?? '').toLowerCase();
      return signer && operator.addressSet.has(signer);
    });
  }, [purchases, operator.addressSet, operator.isLoading]);

  const counts = useMemo(
    () => ({ purchases: myPurchases.length }),
    [myPurchases]
  );

  return (
    <Layout>
      <ServicesSubNav
        active="purchases"
        counts={counts}
        onNewListing={() => setDialogOpen(true)}
      />

      <WalletStrip
        address={primaryWallet}
        agentCount={operator.agents.length}
      />

      <Card>
        <SectionHeader>
          <SectionTitle>Active purchases</SectionTitle>
          <CountChip>{myPurchases.length}</CountChip>
          <Box css={{ flex: 1 }} />
          <Text css={{ fontSize: '$2', color: '$body' }}>
            today · <Mono css={{ color: '$obolGreen' }}>0.00 USDC</Mono> spent
          </Text>
          <FilterBtn type="button">Filter</FilterBtn>
        </SectionHeader>

        {operator.addressSet.size > 0 ? (
          <Text
            css={{
              fontSize: '$1',
              color: '$muted',
              marginBottom: '$xs',
            }}
          >
            Showing purchases signed by {operator.agents.length} agent wallet
            {operator.agents.length === 1 ? '' : 's'}
            {address ? ' + your connected wallet' : ''}.
          </Text>
        ) : (
          <Text css={{ fontSize: '$2', color: '$muted' }}>
            No agent wallets discovered yet and no browser wallet connected —
            showing all {purchases.length} purchase
            {purchases.length === 1 ? '' : 's'} on this stack.
          </Text>
        )}

        {err ? (
          <Text css={{ fontSize: '$2', color: '#DD603C' }}>{err}</Text>
        ) : loading ? (
          <Text css={{ fontSize: '$2', color: '$muted' }}>
            Loading purchases…
          </Text>
        ) : myPurchases.length === 0 ? (
          <Text css={{ fontSize: '$2', color: '$muted' }}>
            No purchases yet. Run{' '}
            <Mono css={{ color: '$obolGreen' }}>buy.py buy</Mono> against any
            x402-gated endpoint to create one.
          </Text>
        ) : (
          <>
            <TableHeaderRow>
              <span>Service</span>
              <span>Chain</span>
              <span style={{ textAlign: 'right' }}>Price</span>
              <span>Auth pool</span>
              <span>Auto-refill</span>
              <span style={{ textAlign: 'right' }}>Actions</span>
            </TableHeaderRow>
            <Box css={{ display: 'flex', flexDirection: 'column', gap: '$xs' }}>
              {myPurchases.map(p => {
                const k = `${p.namespace}/${p.name}`;
                return (
                  <PurchaseRow
                    key={k}
                    p={p}
                    expanded={expandedKey === k}
                    onToggle={() =>
                      setExpandedKey(prev => (prev === k ? null : k))
                    }
                  />
                );
              })}
            </Box>
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
