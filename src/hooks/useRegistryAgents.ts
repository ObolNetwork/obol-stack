'use client';

import { useState, useEffect, useCallback } from 'react';
import type { Agent8004 } from '@/types/registry';
import type { ChainOption } from '@/components/atoms/ChainSelector';
import {
  chainIdForRegistryFilter,
  rankRegistryAgents,
} from '@/lib/services/registry-display';

const PAGE_SIZE = 10;

export function useRegistryAgents(
  search?: string,
  chainFilter?: ChainOption,
  protocolFilter?: string,
  x402Only?: boolean
) {
  const [agents, setAgents] = useState<Agent8004[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [hasMore, setHasMore] = useState(false);

  const chainId = chainIdForRegistryFilter(chainFilter);

  const fetchAgents = useCallback(
    async (currentPage: number) => {
      setError(null);
      try {
        const params = new URLSearchParams({
          limit: String(PAGE_SIZE),
          page: String(currentPage),
        });
        if (chainId) params.set('chainId', String(chainId));
        if (search) params.set('search', search);
        if (protocolFilter) params.set('protocol', protocolFilter);
        if (x402Only) params.set('x402_supported', 'true');
        const res = await fetch(`/api/registry/agents?${params}`);
        if (!res.ok) {
          const data = await res.json();
          throw new Error(data.error || `HTTP ${res.status}`);
        }
        const data = await res.json();
        // Default order from the API is arbitrary; rank by quality signals.
        // Applies to every fetch, so ranking persists within search/filter
        // results too.
        setAgents(rankRegistryAgents(data.agents || []));
        setTotal(data.total ?? 0);
        setHasMore(data.hasMore ?? false);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch agents');
      } finally {
        setLoading(false);
      }
    },
    [search, chainId, protocolFilter, x402Only]
  );

  // Reset to page 1 when any filter changes
  useEffect(() => {
    setPage(1);
  }, [search, chainId, protocolFilter, x402Only]);

  useEffect(() => {
    fetchAgents(page);
    const interval = setInterval(() => fetchAgents(page), 30000);
    return () => clearInterval(interval);
  }, [fetchAgents, page]);

  const goToPage = (p: number) => setPage(p);
  const nextPage = () => setPage(p => p + 1);
  const prevPage = () => setPage(p => Math.max(1, p - 1));
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return {
    agents,
    loading,
    error,
    page,
    totalPages,
    hasMore,
    total,
    goToPage,
    nextPage,
    prevPage,
    refetch: () => fetchAgents(page),
  };
}
