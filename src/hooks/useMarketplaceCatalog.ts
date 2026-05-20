'use client';

import { useQuery } from '@tanstack/react-query';

import type {
  MarketplaceFilters,
  ServiceListResponse,
} from '@/lib/marketplace/types';

const REFRESH_MS = 30_000;

/**
 * Serializes MarketplaceFilters into the same query string the API route
 * accepts. Kept in one place so the cache key (the resulting URL) is stable
 * across the page and any nested components that re-query with the same
 * filters.
 */
export function filtersToSearchParams(
  filters: MarketplaceFilters
): URLSearchParams {
  const p = new URLSearchParams();
  if (filters.type?.length) p.set('type', filters.type.join(','));
  if (filters.network?.length) p.set('network', filters.network.join(','));
  if (filters.asset?.length) p.set('asset', filters.asset.join(','));
  if (filters.minPrice) p.set('minPrice', filters.minPrice);
  if (filters.maxPrice) p.set('maxPrice', filters.maxPrice);
  if (filters.q) p.set('q', filters.q);
  if (filters.sort) p.set('sort', filters.sort);
  if (filters.page) p.set('page', String(filters.page));
  if (filters.pageSize) p.set('pageSize', String(filters.pageSize));
  return p;
}

export function useMarketplaceCatalog(filters: MarketplaceFilters) {
  const qs = filtersToSearchParams(filters).toString();
  return useQuery<ServiceListResponse, Error>({
    queryKey: ['marketplace', 'services', qs],
    queryFn: async () => {
      const res = await fetch(
        `/api/marketplace/services${qs ? `?${qs}` : ''}`,
        { headers: { accept: 'application/json' } }
      );
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      return (await res.json()) as ServiceListResponse;
    },
    staleTime: REFRESH_MS,
    refetchInterval: REFRESH_MS,
    refetchOnWindowFocus: false,
  });
}
