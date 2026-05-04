'use client';

import { useState, useEffect, useCallback } from 'react';
import type { ServiceOffer } from '@/types/sell';

const REFRESH_INTERVAL_MS = 30_000;

export function useServiceOffers() {
  const [offers, setOffers] = useState<ServiceOffer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchOffers = useCallback(async () => {
    try {
      setError(null);
      const response = await fetch('/api/sell/list');
      if (!response.ok) {
        throw new Error('Failed to fetch service offers');
      }
      const data = await response.json();
      setOffers(data.offers || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchOffers();
    const id = setInterval(fetchOffers, REFRESH_INTERVAL_MS);
    return () => clearInterval(id);
  }, [fetchOffers]);

  return { offers, loading, error, refetch: fetchOffers };
}
