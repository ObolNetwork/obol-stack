/**
 * Pure filter/sort/paginate + facet aggregation for the marketplace listing.
 *
 * No I/O, no React, no Next runtime — these are exercised directly by node
 * --test unit tests and called from the API Route Handler. Keeping the
 * server-side filter logic in one place means both views see identical
 * results and adding /api/marketplace/services?type=agent later is free.
 */

import type {
  FacetBucket,
  MarketplaceFacets,
  MarketplaceFilters,
  MarketplaceSort,
  MarketplaceSource,
  ServiceListItem,
} from './types';

/**
 * Source classification: anything coming from an external indexer
 * (8004scan) is "remote"; cluster-published offers are "local".
 * Keyed off the namespace prefix the catalog/mapper sets.
 */
function itemSource(item: ServiceListItem): MarketplaceSource {
  return item.namespace.startsWith('erc8004:') ? 'remote' : 'local';
}

const DEFAULT_PAGE_SIZE = 24;
const MAX_PAGE_SIZE = 200;

export function filterItems(
  items: readonly ServiceListItem[],
  filters: MarketplaceFilters
): ServiceListItem[] {
  const typeSet = toSet(filters.type);
  const networkSet = toSet(filters.network);
  const assetSet = toSet(filters.asset?.map(a => a.toUpperCase()));
  const sourceSet = toSet(filters.source);
  const min = parseDecimal(filters.minPrice);
  const max = parseDecimal(filters.maxPrice);
  const q = filters.q?.trim().toLowerCase();

  return items.filter(it => {
    if (typeSet && !typeSet.has(it.type)) return false;
    if (sourceSet && !sourceSet.has(itemSource(it))) return false;
    if (networkSet && !networkSet.has(it.network.caip2)) return false;
    if (assetSet && !assetSet.has(it.asset.symbol.toUpperCase())) return false;
    if (min !== undefined || max !== undefined) {
      const p = parseDecimal(it.price);
      if (p === undefined) return false;
      if (min !== undefined && p < min) return false;
      if (max !== undefined && p > max) return false;
    }
    if (q && !matchesText(it, q)) return false;
    return true;
  });
}

export function sortItems(
  items: readonly ServiceListItem[],
  sort: MarketplaceSort = 'recent'
): ServiceListItem[] {
  const out = items.slice();
  switch (sort) {
    case 'price_asc':
      out.sort(
        (a, b) =>
          (parseDecimal(a.price) ?? Infinity) -
          (parseDecimal(b.price) ?? Infinity)
      );
      break;
    case 'price_desc':
      out.sort(
        (a, b) =>
          (parseDecimal(b.price) ?? -Infinity) -
          (parseDecimal(a.price) ?? -Infinity)
      );
      break;
    case 'name':
      out.sort((a, b) => a.name.localeCompare(b.name));
      break;
    case 'recent':
    default:
      // Stable tiebreaker: live offers float to the top, then alphabetical.
      out.sort((a, b) => {
        if (a.status === b.status) return a.name.localeCompare(b.name);
        if (a.status === 'live') return -1;
        if (b.status === 'live') return 1;
        return a.name.localeCompare(b.name);
      });
      break;
  }
  return out;
}

export interface Paginated<T> {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
}

export function paginate<T>(
  items: readonly T[],
  page = 1,
  pageSize = DEFAULT_PAGE_SIZE
): Paginated<T> {
  const total = items.length;
  const safeSize = Math.min(Math.max(1, pageSize | 0), MAX_PAGE_SIZE);
  const pages = Math.max(1, Math.ceil(total / safeSize));
  const safePage = Math.min(Math.max(1, page | 0), pages);
  const start = (safePage - 1) * safeSize;
  return {
    items: items.slice(start, start + safeSize),
    total,
    page: safePage,
    pageSize: safeSize,
  };
}

/**
 * Build facets from the FULL (unfiltered) set so the UI can render
 * "(N)" counts that don't collapse to zero as the user toggles filters.
 */
export function computeFacets(
  items: readonly ServiceListItem[]
): MarketplaceFacets {
  return {
    type: bucketize(items, it => it.type),
    network: bucketize(items, it => it.network.caip2),
    asset: bucketize(items, it => it.asset.symbol.toUpperCase()),
    source: bucketize(items, itemSource),
  };
}

function bucketize<T>(
  items: readonly T[],
  pick: (item: T) => string
): FacetBucket[] {
  const counts = new Map<string, number>();
  for (const it of items) {
    const v = pick(it);
    counts.set(v, (counts.get(v) ?? 0) + 1);
  }
  return Array.from(counts.entries())
    .map(([value, count]) => ({ value, count }))
    .sort((a, b) => b.count - a.count || a.value.localeCompare(b.value));
}

function toSet(values?: string[]): Set<string> | undefined {
  if (!values || values.length === 0) return undefined;
  return new Set(values);
}

function parseDecimal(raw?: string): number | undefined {
  if (raw === undefined || raw === null || raw === '') return undefined;
  const n = Number(raw);
  return Number.isFinite(n) ? n : undefined;
}

function matchesText(item: ServiceListItem, q: string): boolean {
  if (item.name.toLowerCase().includes(q)) return true;
  if (item.description.toLowerCase().includes(q)) return true;
  if (item.modelOrRuntime.toLowerCase().includes(q)) return true;
  return item.skills.some(s => s.toLowerCase().includes(q));
}
