'use client';

import { useEffect, useState } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

import { Button } from '@/components/atoms/Button';
import { ChainIcon } from '@/components/atoms/ChainIcon';
import { CopyButton } from '@/components/atoms/CopyButton';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import { toChainName } from '@/lib/marketplace/chain';
import {
  formatAge,
  formatSettlementTimestamp,
} from '@/lib/marketplace/settlement';
import { tones } from '@/lib/ui/tones';
import type { PurchaseRequest } from '@/types/purchase';

import { Identicon } from '../Identicon';
import { FieldLabel, Mono, RowBody } from '../ServicesChrome';
import { StatusPill } from '../StatusPill';

export const PURCHASE_TABLE_COLS =
  'minmax(220px, 1.4fr) 130px 110px minmax(220px, 1.4fr) 150px 100px';

const RowCard = styled('div', {
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  overflow: 'hidden',

  variants: {
    stuck: {
      true: {
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
  background: tones.danger.bg,
  borderBottom: `1px solid ${tones.danger.border}`,
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
      true: { background: tones.warning.text },
    },
    empty: {
      true: { background: tones.danger.text },
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
export function SettlementRow({
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

export function PurchaseExpansion({ p }: { p: PurchaseRequest }) {
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
            {/* Controller writes status.spent (consumed auths); totalSpent is unused. */}
            <Mono css={{ color: '$light' }}>{p.status.spent}</Mono>
          </Box>
          <Box>
            <FieldLabel>Total signed</FieldLabel>
            <Mono css={{ color: '$light' }}>{p.status.totalSigned}</Mono>
          </Box>
          <Box>
            <FieldLabel>Wallet bal.</FieldLabel>
            {/* walletBalance is on the CRD but not populated during reconciliation yet. */}
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

// Expand/collapse chevron — Button atom (bare/sm/iconOnly) carrying only the
// open-state rotation.
const ChevronBtn = styled(Button, {
  flexShrink: 0,
  transition: 'transform 200ms, color 150ms',

  variants: {
    open: {
      true: { transform: 'rotate(180deg)' },
    },
  },

  defaultVariants: {
    variant: 'bare',
    size: 'sm',
    iconOnly: true,
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

export interface PurchaseRowProps {
  p: PurchaseRequest;
  expanded: boolean;
  onToggle: () => void;
}

export function PurchaseRow({ p, expanded, onToggle }: PurchaseRowProps) {
  const remaining = p.status.remaining;
  const total = p.spec.count || p.status.totalSigned || 0;
  const refillOn = p.spec.autoRefill?.enabled ?? false;
  const chainIcon = toChainName(p.spec.payment.network);
  const stuck = remaining === 0 && !refillOn;
  const fillPct = total > 0 ? Math.min(100, (remaining / total) * 100) : 0;
  const threshold = p.spec.autoRefill?.threshold ?? 0;
  const low = total > 0 && remaining > 0 && remaining <= threshold;
  const assetSymbol =
    p.spec.payment.assetSymbol ?? p.spec.payment.asset.slice(0, 6);

  return (
    <RowCard stuck={stuck}>
      {stuck ? (
        <StuckStrip>
          <Box css={{ flex: 1 }}>
            Auth pool empty and refill is off. Top up or arm auto-refill to keep
            the route live.
          </Box>
          <Button type="button" variant="ghost" size="sm" pill>
            Logs
          </Button>
        </StuckStrip>
      ) : null}
      <RowBody css={{ gridTemplateColumns: PURCHASE_TABLE_COLS }}>
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
            <ObolAmountSwitch
              amount={p.spec.payment.price}
              symbol={assetSymbol}
              align="right"
            />
          </Mono>
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
          <StatusPill
            tone={refillOn ? 'success' : 'neutral'}
            label={refillOn ? 'on' : 'off'}
          />
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

        <Box css={{ textAlign: 'right' }}>
          {/* Top-up drawer not wired yet; use buy.py buy / obol buy inference for now. */}
          <Button
            type="button"
            size="sm"
            disabled
            variant={stuck ? 'primary' : 'secondary'}
          >
            {stuck ? `Top up ${p.spec.autoRefill?.count ?? 50}` : 'Top up'}
          </Button>
          <Text
            css={{
              fontSize: '$1',
              color: '$muted',
              fontFamily: 'monospace',
              marginTop: 4,
            }}
          >
            Not configured
          </Text>
        </Box>
      </RowBody>
      {expanded ? <PurchaseExpansion p={p} /> : null}
    </RowCard>
  );
}
