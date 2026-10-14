'use client';

import { useEffect, useMemo, useState } from 'react';
import { useAccount } from 'wagmi';
import type { AgentRuntime } from '@/types/agent';

export interface AgentWallet {
  instanceId: string;
  runtime: AgentRuntime;
  addresses: string[];
}

export interface UseOperatorWalletsResult {
  /** Lower-cased union of agent wallets + connected browser wallet. */
  addressSet: ReadonlySet<string>;
  /** Per-agent breakdown for WalletStrip + tooltips. */
  agents: AgentWallet[];
  /** Connected browser wallet (may be undefined). */
  connected: string | undefined;
  isLoading: boolean;
  error: string | null;
}

interface ApiResponse {
  agents: AgentWallet[];
  addresses: string[];
  error?: string;
}

/**
 * The operator's owned-wallet set: every wallet controlled by an agent on
 * this stack, plus the browser-connected wallet (if any).
 *
 * Used by /marketplace/listings to filter offers by `payment.payTo` and by
 * /marketplace/purchases to filter by `status.signerAddress`. The mental
 * model: "everything I control on this stack" — most purchases come from
 * agent wallets, listings can come from either.
 *
 * Polls every 60s to pick up newly-spawned agents.
 */
export function useOperatorWallets(): UseOperatorWalletsResult {
  const { address } = useAccount();
  const [agents, setAgents] = useState<AgentWallet[]>([]);
  const [agentAddrs, setAgentAddrs] = useState<string[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const fetchWallets = async () => {
      try {
        const resp = await fetch('/api/agents/wallets', { cache: 'no-store' });
        const body = (await resp.json()) as ApiResponse;
        if (cancelled) return;
        if (!resp.ok) throw new Error(body.error ?? `HTTP ${resp.status}`);
        setAgents(body.agents ?? []);
        setAgentAddrs(body.addresses ?? []);
        setError(null);
      } catch (e) {
        if (cancelled) return;
        setError(
          e instanceof Error ? e.message : 'Failed to load agent wallets'
        );
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    };
    fetchWallets();
    const id = setInterval(fetchWallets, 60_000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  const addressSet = useMemo(() => {
    const set = new Set(agentAddrs.map(a => a.toLowerCase()));
    if (address) set.add(address.toLowerCase());
    return set;
  }, [agentAddrs, address]);

  return { addressSet, agents, connected: address, isLoading, error };
}
