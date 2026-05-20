import { NextResponse, type NextRequest } from 'next/server';

import { applyPipeline, fetchCatalog, parseFilters } from '@/lib/marketplace';

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
export async function GET(req: NextRequest) {
  try {
    const filters = parseFilters(req.nextUrl.searchParams);
    const snapshot = await fetchCatalog();
    const response = applyPipeline(snapshot.items, filters, snapshot.fetchedAt);
    return NextResponse.json(response);
  } catch (error) {
    console.error('[Marketplace] list failed:', error);
    return NextResponse.json(
      { error: 'Failed to load marketplace catalog' },
      { status: 502 }
    );
  }
}
