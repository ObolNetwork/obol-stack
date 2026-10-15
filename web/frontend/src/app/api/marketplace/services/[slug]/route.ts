import { NextResponse, type NextRequest } from 'next/server';

import { fetchCatalog } from '@/lib/marketplace';
import {
  STACK_UNREACHABLE,
  isStackUnreachable,
} from '@/lib/marketplace/errors';
import { fetchErc8004BySlug } from '@/lib/marketplace/erc8004-source';
import { isErc8004Listing } from '@/lib/marketplace/types';
import { AgentClient } from '@/lib/services/agents';
import {
  AGENT_REGISTRATION_PATH,
  publicResourceUrl,
} from '@/lib/services/public-storefront';
import { SellClient } from '@/lib/services/sell';
import type { ServiceDetail, ServiceListItem } from '@/lib/marketplace/types';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/marketplace/services/[slug]
 *
 * Resolves Obol catalog rows first, then ERC-8004 registry rows. Cluster
 * listings are enriched with ServiceOffer conditions; public registry rows
 * are returned as-is from the 8004scan snapshot.
 */
export async function GET(
  req: NextRequest,
  ctx: { params: Promise<{ slug: string }> }
) {
  try {
    const { slug } = await ctx.params;
    const decoded = decodeURIComponent(slug);

    const resolved = await resolveListing(decoded);
    if (!resolved) {
      return NextResponse.json({ error: 'Not found' }, { status: 404 });
    }

    const { base } = resolved;
    const conditions = isErc8004Listing(base)
      ? []
      : await safeConditions(base.namespace, base.name);
    const wellKnownUrl = await buildWellKnownUrl(base);

    const detail: ServiceDetail = {
      ...base,
      conditions,
      wellKnownUrl,
      paymentSample: {
        scheme: 'exact',
        network: base.network.alias || base.network.caip2,
        payTo: base.payTo,
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

async function resolveListing(
  slug: string
): Promise<{ base: ServiceListItem } | null> {
  const catalog = await fetchCatalog();
  const fromCatalog = catalog.items.find(it => it.slug === slug);
  if (fromCatalog) return { base: fromCatalog };

  const fromRegistry = await fetchErc8004BySlug(slug);
  if (fromRegistry) return { base: fromRegistry };

  return null;
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

async function buildWellKnownUrl(base: ServiceListItem): Promise<string> {
  if (isErc8004Listing(base)) {
    return 'https://www.8004scan.io';
  }

  const agentClient = new AgentClient();
  const tunnelURL = await agentClient.getTunnelURL();
  return publicResourceUrl(tunnelURL, AGENT_REGISTRATION_PATH);
}
