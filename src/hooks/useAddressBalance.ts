'use client';

import { useState, useEffect } from 'react';
import type { AddressBalance } from '@/types/openclaw';

const ERPC_URL = process.env.NEXT_PUBLIC_ERPC_URL ?? 'https://obol.stack/rpc';
const NETWORK_PRIORITY = ['mainnet', 'sepolia', 'hoodi'];
const TIMEOUT_MS = 3000;

function formatEth(wei: bigint): string {
  const eth = Number(wei) / 1e18;
  if (eth === 0) return '0';
  if (eth < 0.001) return '<0.001';
  return eth.toFixed(3).replace(/\.?0+$/, '');
}

/**
 * Fetches ETH balance for an address across connected eRPC networks.
 * Returns the first network with a non-zero balance (mainnet preferred).
 * Returns null if eRPC is unreachable or any error occurs — never throws.
 */
export function useAddressBalance(address: string | null): {
  balance: AddressBalance | null;
} {
  const [balance, setBalance] = useState<AddressBalance | null>(null);

  useEffect(() => {
    if (!address) return;

    const controller = new AbortController();

    async function fetchBalance() {
      try {
        // 1. Discover healthy networks from eRPC status
        const statusRes = await fetch(ERPC_URL, {
          signal: controller.signal,
        });
        if (!statusRes.ok) return;

        const status = await statusRes.json();
        // eRPC raw response uses `rpc` for the networks array
        const healthyNetworks: string[] = (status.rpc ?? [])
          .filter((n: { state: string }) => n.state === 'OK')
          .map((n: { alias: string }) => n.alias);

        // 2. Sort by priority: mainnet first, then known testnets
        const sorted = NETWORK_PRIORITY.filter(n =>
          healthyNetworks.includes(n)
        );
        if (sorted.length === 0) return;

        // 3. Check balance on each network sequentially to avoid hammering
        for (const network of sorted) {
          try {
            const rpcRes = await fetch(`${ERPC_URL}/${network}`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              signal: controller.signal,
              body: JSON.stringify({
                jsonrpc: '2.0',
                id: 1,
                method: 'eth_getBalance',
                params: [address, 'latest'],
              }),
            });

            if (!rpcRes.ok) continue;

            const rpcData = await rpcRes.json();
            if (rpcData.error) continue;

            const weiHex = rpcData.result;
            const wei = BigInt(weiHex);
            if (wei > 0n) {
              setBalance({ balance: formatEth(wei), network });
              return;
            }
          } catch {
            continue;
          }
        }

        // 4. All networks returned 0 — show 0 on highest-priority network
        setBalance({ balance: '0', network: sorted[0] });
      } catch {
        // eRPC entirely unreachable — don't show balance
        setBalance(null);
      }
    }

    const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
    fetchBalance().finally(() => clearTimeout(timer));

    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [address]);

  return { balance };
}
