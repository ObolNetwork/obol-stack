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

function WalletStrip({ address }: { address: string | undefined }) {
  return (
    <Card css={{ padding: '$md' }}>
      <StripGrid>
        <StripCell first>
          <FieldLabel>Agent wallet</FieldLabel>
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
              Connect a wallet from the top nav.
            </Text>
          )}
        </StripCell>

        {(['base', 'sepolia', 'mainnet'] as const).map(c => {
          const label = c === 'sepolia' ? 'base-sepolia' : c;
          const niceName =
            c === 'sepolia'
              ? 'Base Sepolia'
              : c === 'base'
                ? 'Base'
                : 'Mainnet';
          return (
            <StripCell key={c} css={{ opacity: 0.55 }}>
              <Flex css={{ gap: 6, alignItems: 'center' }}>
                <ChainIcon chain={label} size={14} />
                <Text css={{ fontSize: '$1', color: '$body' }}>{niceName}</Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono
                  css={{
                    fontSize: '$5',
                    color: '$muted',
                    fontWeight: '$semibold',
                  }}
                >
                  0.00
                </Mono>
                <Text css={{ fontSize: '$1', color: '$body' }}>USDC</Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono css={{ fontSize: '$3', color: '$muted' }}>0.00</Mono>
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
          <Mono css={{ fontSize: '$2', color: '$light' }}>—</Mono>
        </StripCell>
      </StripGrid>
    </Card>
  );
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

function PurchaseRow({ p }: { p: PurchaseRequest }) {
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
        <Flex css={{ alignItems: 'center', gap: 12, minWidth: 0 }}>
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
    </RowCard>
  );
}

// ── Main page ──────────────────────────────────────────────────────────

export function MyPurchasesPage() {
  const { address } = useAccount();
  const [purchases, setPurchases] = useState<PurchaseRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);

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

  const myPurchases = useMemo(() => {
    if (!address) return purchases;
    const lower = address.toLowerCase();
    return purchases.filter(
      p => (p.status.signerAddress ?? '').toLowerCase() === lower
    );
  }, [purchases, address]);

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

      <WalletStrip address={address} />

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

        {!address ? (
          <Text css={{ fontSize: '$2', color: '$muted' }}>
            Connect a wallet in the top nav to see only your purchases. Showing
            all {purchases.length} purchases on this stack.
          </Text>
        ) : null}

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
              {myPurchases.map(p => (
                <PurchaseRow key={`${p.namespace}/${p.name}`} p={p} />
              ))}
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
