/**
 * Query-param parsing + the end-to-end filter→sort→paginate→facets pipeline
 * the API route uses. Kept thin (no fetch) so the same pipeline can be unit
 * tested or reused server-side from React Server Components without going
 * through HTTP.
 */

import { computeFacets, filterItems, paginate, sortItems } from './filter';
import type {
  MarketplaceFilters,
  MarketplaceSort,
  ServiceListItem,
  ServiceListResponse,
} from './types';
import type { ServiceOfferType } from '@/types/sell';

const ALLOWED_TYPES: readonly ServiceOfferType[] = [
  'inference',
  'fine-tuning',
  'http',
  'agent',
] as const;

const ALLOWED_SORTS: readonly MarketplaceSort[] = [
  'price_asc',
  'price_desc',
  'name',
  'recent',
] as const;

export function parseFilters(
  params: URLSearchParams | Record<string, string | string[] | undefined>
): MarketplaceFilters {
  const get = (key: string): string[] => readMulti(params, key);
  const one = (key: string): string | undefined => readSingle(params, key);

  return {
    type: pickEnums(get('type'), ALLOWED_TYPES),
    network: get('network').filter(Boolean),
    asset: get('asset').filter(Boolean),
    minPrice: one('minPrice'),
    maxPrice: one('maxPrice'),
    q: one('q'),
    sort: pickEnum(one('sort'), ALLOWED_SORTS) ?? 'recent',
    page: toInt(one('page'), 1),
    pageSize: toInt(one('pageSize'), 24),
  };
}

export function applyPipeline(
  items: readonly ServiceListItem[],
  filters: MarketplaceFilters,
  cachedAt: string
): ServiceListResponse {
  const filtered = filterItems(items, filters);
  const sorted = sortItems(filtered, filters.sort ?? 'recent');
  const page = paginate(sorted, filters.page, filters.pageSize);
  return {
    items: page.items,
    total: page.total,
    // Facets are computed on the UNFILTERED set so users can see what else is
    // available without filters collapsing themselves to zero.
    facets: computeFacets(items),
    cachedAt,
  };
}

function readMulti(
  params: URLSearchParams | Record<string, string | string[] | undefined>,
  key: string
): string[] {
  if (params instanceof URLSearchParams) {
    // Support both ?type=a&type=b and ?type=a,b
    const all = params.getAll(key);
    return all.flatMap(v =>
      v
        .split(',')
        .map(s => s.trim())
        .filter(Boolean)
    );
  }
  const raw = params[key];
  if (raw === undefined) return [];
  if (Array.isArray(raw)) {
    return raw.flatMap(v =>
      v
        .split(',')
        .map(s => s.trim())
        .filter(Boolean)
    );
  }
  return raw
    .split(',')
    .map(s => s.trim())
    .filter(Boolean);
}

function readSingle(
  params: URLSearchParams | Record<string, string | string[] | undefined>,
  key: string
): string | undefined {
  if (params instanceof URLSearchParams) {
    const v = params.get(key);
    return v === null ? undefined : v;
  }
  const raw = params[key];
  if (Array.isArray(raw)) return raw[0];
  return raw;
}

function pickEnums<T extends string>(
  values: string[],
  allowed: readonly T[]
): T[] | undefined {
  const set = new Set(allowed);
  const out = values.filter((v): v is T => set.has(v as T));
  return out.length > 0 ? out : undefined;
}

function pickEnum<T extends string>(
  value: string | undefined,
  allowed: readonly T[]
): T | undefined {
  if (!value) return undefined;
  return allowed.includes(value as T) ? (value as T) : undefined;
}

function toInt(value: string | undefined, fallback: number): number {
  if (!value) return fallback;
  const n = Number.parseInt(value, 10);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}
