'use client';

import { useMemo, useState } from 'react';
import { Box, Text, styled } from '@obolnetwork/obol-ui';
import { useAccount } from 'wagmi';

import { Button } from '@/components/atoms/Button';
import { usePolledFetch } from '@/hooks/usePolledFetch';
import { useOperatorWallets } from '@/hooks/useOperatorWallets';
import { tones } from '@/lib/ui/tones';
import type { PurchaseListResponse, PurchaseRequest } from '@/types/purchase';

import { ServicesSubNav } from './ServicesSubNav';
import { NewListingDialog } from './NewListingDialog';
import {
  ServicesPanel,
  CountChip,
  Mono,
  SectionHeader,
  SectionTitle,
  TableHeaderRow,
} from './ServicesChrome';
import { WalletStrip } from './purchases/WalletStrip';
import { PURCHASE_TABLE_COLS, PurchaseRow } from './purchases/PurchaseRow';

const Layout = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

// Stable fallback so memos don't re-run while the first poll is in flight.
const NO_PURCHASES: PurchaseRequest[] = [];

export function MyPurchasesPage() {
  const { address } = useAccount();
  const operator = useOperatorWallets();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [expandedKey, setExpandedKey] = useState<string | null>(null);

  const {
    data,
    loading,
    error: err,
  } = usePolledFetch<PurchaseRequest[]>('/api/marketplace/purchases', {
    mapResponse: body =>
      (body as PurchaseListResponse).purchases ?? NO_PURCHASES,
    errorFallback: 'Failed to load purchases',
    loadingOnPoll: true,
  });
  const purchases = data ?? NO_PURCHASES;

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

      <ServicesPanel>
        <SectionHeader>
          <SectionTitle>Active purchases</SectionTitle>
          <CountChip>{myPurchases.length}</CountChip>
          <Box css={{ flex: 1 }} />
          <Text css={{ fontSize: '$2', color: '$body' }}>
            today · <Mono css={{ color: '$obolGreen' }}>0.00 USDC</Mono> spent
          </Text>
          <Button type="button" variant="ghost" size="sm" pill>
            Filter
          </Button>
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
          <Text css={{ fontSize: '$2', color: tones.danger.text }}>{err}</Text>
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
            <TableHeaderRow css={{ gridTemplateColumns: PURCHASE_TABLE_COLS }}>
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
      </ServicesPanel>

      <NewListingDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
      />
    </Layout>
  );
}
