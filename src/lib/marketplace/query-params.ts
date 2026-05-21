/**
 * Pure serializer: MarketplaceFilters → URLSearchParams. Used by the
 * react-query hook to build the cache key + request URL. Kept in
 * `lib/marketplace` (alongside the pipeline parser) so it's testable
 * under node:test without pulling in next/react.
 */

import type { MarketplaceFilters } from './types';

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
