import { NextResponse, type NextRequest } from 'next/server';

import { applyPipeline, fetchCatalog, parseFilters } from '@/lib/marketplace';
import {
  STACK_UNREACHABLE,
  isStackUnreachable,
} from '@/lib/marketplace/errors';
import { fetchErc8004Registry } from '@/lib/marketplace/erc8004-source';
import type { Erc8004Snapshot } from '@/lib/marketplace/erc8004-source';

export const runtime = 'nodejs';
// 30s server cache window. ISR not used because filters live in the query
// string and we don't want Next caching the cross-product.
export const dynamic = 'force-dynamic';

/**
 * GET /api/marketplace/services
 *
 * Query params (all optional):
 *   type      = inference|agent|http|fine-tuning  (multi, comma- or repeat-form)
 *   network   = eip155:1 | eip155:8453 | eip155:84532 …
 *   asset     = OBOL | USDC
 *   minPrice, maxPrice  (decimal strings, inclusive)
 *   q         = full-text over name / description / model / skills
 *   sort      = price_asc | price_desc | name | recent  (default: recent)
 *   page, pageSize       (defaults: 1, 24)
 *
 * Returns ServiceListResponse { items, total, facets, cachedAt }.
 *
 * Catalog source: controller-published /api/services.json (in-cluster
 * service `obol-skill-md.x402.svc.cluster.local:8080`). Override the URL via
 * MARKETPLACE_CATALOG_URL for local dev.
 */
// In-memory ERC-8004 snapshot cache (5 min). On-chain scans + per-agent
// metadata fetches are expensive; we don't want every navigation to
// re-issue all of that. Bound to the server process — fine for one-node
// dev pods and resets cleanly on rollout.
let erc8004Cache: { snapshot: Erc8004Snapshot; cachedAt: number } | null = null;
const ERC8004_TTL_MS = 5 * 60 * 1000;

async function loadErc8004Snapshot(): Promise<Erc8004Snapshot> {
  if (erc8004Cache && Date.now() - erc8004Cache.cachedAt < ERC8004_TTL_MS) {
    return erc8004Cache.snapshot;
  }
  const snapshot = await fetchErc8004Registry();
  erc8004Cache = { snapshot, cachedAt: Date.now() };
  return snapshot;
}

export async function GET(req: NextRequest) {
  try {
    const filters = parseFilters(req.nextUrl.searchParams);
    const snapshot =
      filters.registry === 'erc8004'
        ? await loadErc8004Snapshot()
        : await fetchCatalog();
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
