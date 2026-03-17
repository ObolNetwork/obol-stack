'use client';

import { useState, useMemo } from 'react';
import type { Agent8004 } from '@/types/registry';
import type { ChainOption } from '@/components/atoms/ChainSelector';

export function useAgentFilters(agents: Agent8004[]) {
  const [x402Only, setX402Only] = useState(false);
  const [protocolFilters, setProtocolFilters] = useState<Set<string>>(
    new Set()
  );
  const [chainFilter, setChainFilter] = useState<ChainOption | undefined>(
    'base-sepolia'
  );

  const filtered = useMemo(() => {
    let result = agents;
    if (x402Only) result = result.filter(a => a.x402_supported);
    if (protocolFilters.size > 0)
      result = result.filter(a =>
        a.supported_protocols.some(p => protocolFilters.has(p))
      );
    if (chainFilter === 'base-sepolia')
      result = result.filter(a => a.is_testnet);
    if (chainFilter === 'base-mainnet')
      result = result.filter(a => !a.is_testnet);
    return result;
  }, [agents, x402Only, protocolFilters, chainFilter]);

  const toggleProtocol = (p: string) => {
    setProtocolFilters(prev => {
      const next = new Set(prev);
      next.has(p) ? next.delete(p) : next.add(p);
      return next;
    });
  };

  const hasActiveFilters =
    x402Only || protocolFilters.size > 0 || !!chainFilter;

  return {
    x402Only,
    setX402Only,
    protocolFilters,
    toggleProtocol,
    chainFilter,
    setChainFilter,
    filtered,
    hasActiveFilters,
  };
}
