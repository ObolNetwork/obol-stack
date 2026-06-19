import { NextResponse, type NextRequest } from 'next/server';

import { applyPipeline, fetchCatalog, parseFilters } from '@/lib/marketplace';
import {
  STACK_UNREACHABLE,
  isStackUnreachable,
} from '@/lib/marketplace/errors';
import { fetchErc8004Page } from '@/lib/marketplace/erc8004-source';
import {
  filterItems,
  sortItems,
  computeFacets,
} from '@/lib/marketplace/filter';
import type { FacetBucket } from '@/lib/marketplace/types';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

const CHAIN_CAIP2: Record<number, string> = {
  1: 'eip155:1',
  8453: 'eip155:8453',
  84532: 'eip155:84532',
};

export async function GET(req: NextRequest) {
  try {
    const filters = parseFilters(req.nextUrl.searchParams);

    if (filters.registry === 'erc8004') {
      // Pass network filter so we only fetch the chains the user selected.
      // Search (q) is also passed to 8004scan for server-side text matching.
      const result = await fetchErc8004Page(
        filters.page ?? 1,
        filters.pageSize ?? 24,
        filters.q,
        filters.network?.length ? filters.network : undefined
      );

      // Apply type/source/asset filters to the current page items.
      // Skip price (min/maxPrice) — ERC-8004 agents have no on-chain price.
      const filtered = filterItems(result.items, {
        ...filters,
        minPrice: undefined,
        maxPrice: undefined,
      });
      const sorted = sortItems(filtered, filters.sort ?? 'recent');

      // Network facets: real per-chain totals from 8004scan metadata.
      // Always include all three chains so the user can switch between them.
      const networkFacets: FacetBucket[] = Object.entries(result.chainTotals)
        .map(([chainId, count]) => ({
          value: CHAIN_CAIP2[Number(chainId)] ?? `eip155:${chainId}`,
          count,
        }))
        .sort((a, b) => b.count - a.count);

      return NextResponse.json({
        items: sorted,
        total: result.total,
        facets: {
          ...computeFacets(filtered),
          network: networkFacets,
        },
        cachedAt: result.fetchedAt,
      });
    }

    const snapshot = await fetchCatalog();
    const response = applyPipeline(snapshot.items, filters, snapshot.fetchedAt);
    return NextResponse.json(response);
  } catch (error) {
    console.error('[Marketplace] list failed:', error);
    if (isStackUnreachable(error)) {
      return NextResponse.json(
        { error: 'Obol Stack unreachable', code: STACK_UNREACHABLE },
        { status: 503 }
      );
    }
    return NextResponse.json(
      { error: 'Failed to load marketplace catalog' },
      { status: 502 }
    );
  }
}
