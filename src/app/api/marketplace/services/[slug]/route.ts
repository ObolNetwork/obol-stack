import { NextResponse, type NextRequest } from 'next/server';

import { fetchCatalog } from '@/lib/marketplace';
import {
  STACK_UNREACHABLE,
  isStackUnreachable,
} from '@/lib/marketplace/errors';
import { SellClient } from '@/lib/services/sell';
import type { ServiceDetail } from '@/lib/marketplace/types';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/marketplace/services/[slug]
 *
 * `slug` is the URL-encoded "<namespace>/<name>" pair the list returns. We
 * resolve the catalog entry first (covers anonymous reads) and then enrich
 * with status.conditions from the live ServiceOffer CR for the detail page.
 *
 * The k8s call is best-effort — if it fails, we still return the catalog row
 * plus empty conditions so the page renders.
 */
export async function GET(
  req: NextRequest,
  ctx: { params: Promise<{ slug: string }> }
) {
  try {
    const { slug } = await ctx.params;
    const decoded = decodeURIComponent(slug);

    const snapshot = await fetchCatalog();
    const base = snapshot.items.find(it => it.slug === decoded);
    if (!base) {
      return NextResponse.json({ error: 'Not found' }, { status: 404 });
    }

    const conditions = await safeConditions(base.namespace, base.name);

    const detail: ServiceDetail = {
      ...base,
      conditions,
      wellKnownUrl: buildWellKnown(req, base.namespace, base.name),
      paymentSample: {
        scheme: 'exact',
        network: base.network.alias || base.network.caip2,
        payTo: '',
        asset: base.asset,
        maxAmountRequired: base.priceAtomicUnits,
      },
    };

    return NextResponse.json(detail);
  } catch (error) {
    console.error('[Marketplace] detail failed:', error);
    if (isStackUnreachable(error)) {
      return NextResponse.json(
        { error: 'Obol Stack unreachable', code: STACK_UNREACHABLE },
        { status: 503 }
      );
    }
    return NextResponse.json(
      { error: 'Failed to load service detail' },
      { status: 502 }
    );
  }
}

async function safeConditions(
  namespace: string,
  name: string
): Promise<ServiceDetail['conditions']> {
  try {
    const sell = new SellClient();
    const offers = await sell.listServiceOffers(namespace);
    const match = offers.find(
      o => o.name === name && o.namespace === namespace
    );
    return match?.conditions ?? [];
  } catch (error) {
    console.warn('[Marketplace] conditions enrichment skipped:', error);
    return [];
  }
}

function buildWellKnown(
  req: NextRequest,
  namespace: string,
  name: string
): string {
  // The controller publishes per-offer ERC-8004 docs at the same host the
  // request came in on. namespace+name is sufficient because the controller
  // only ever publishes one document per offer.
  const origin = req.nextUrl.origin;
  const qs = new URLSearchParams({ namespace, name }).toString();
  return `${origin}/.well-known/agent-registration.json?${qs}`;
}
