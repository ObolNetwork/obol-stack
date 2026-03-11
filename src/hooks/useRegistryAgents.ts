'use client';

import { useState, useEffect, useCallback } from 'react';
import type { Agent8004 } from '@/types/registry';

export function useRegistryAgents(
  limit = 20,
  search?: string,
  protocol?: string
) {
  const [agents, setAgents] = useState<Agent8004[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchAgents = useCallback(async () => {
    try {
      const params = new URLSearchParams({ limit: String(limit) });
      if (search) params.set('search', search);
      if (protocol) params.set('protocol', protocol);
      const res = await fetch(`/api/registry/agents?${params}`);
      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      const data = await res.json();
      setAgents(data.agents || []);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to fetch agents');
    } finally {
      setLoading(false);
    }
  }, [limit, search, protocol]);

  useEffect(() => {
    fetchAgents();
    const interval = setInterval(fetchAgents, 30000);
    return () => clearInterval(interval);
  }, [fetchAgents]);

  return { agents, loading, error, refetch: fetchAgents };
}
