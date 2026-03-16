'use client';

import { useState, useEffect, useCallback } from 'react';
import type { Agent8004 } from '@/types/registry';

const PAGE_SIZE = 20;

export function useRegistryAgents(search?: string) {
  const [agents, setAgents] = useState<Agent8004[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [hasMore, setHasMore] = useState(false);

  const fetchAgents = useCallback(
    async (currentPage: number) => {
      setError(null);
      try {
        setLoading(true);
        const params = new URLSearchParams({
          limit: String(PAGE_SIZE),
          page: String(currentPage),
        });
        if (search) params.set('search', search);
        const res = await fetch(`/api/registry/agents?${params}`);
        if (!res.ok) {
          const data = await res.json();
          throw new Error(data.error || `HTTP ${res.status}`);
        }
        const data = await res.json();
        setAgents(data.agents || []);
        setTotal(data.total ?? 0);
        setHasMore(data.hasMore ?? false);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch agents');
      } finally {
        setLoading(false);
      }
    },
    [search]
  );

  // Reset to page 1 when search changes
  useEffect(() => {
    setPage(1);
  }, [search]);

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
