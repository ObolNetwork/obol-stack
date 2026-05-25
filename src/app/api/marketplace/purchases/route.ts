import { NextResponse } from 'next/server';
import { PurchasesClient } from '@/lib/services/purchases';
import { SellClient } from '@/lib/services/sell';
import { PrometheusClient } from '@/lib/services/prometheus';
import { offerNameFromEndpoint } from '@/lib/marketplace/settlement';
import type { PurchaseListResponse, PurchaseRequest } from '@/types/purchase';
import type { ServiceOffer } from '@/types/sell';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/marketplace/purchases
 * Lists all PurchaseRequest CRs across the cluster. The frontend's
 * MyPurchasesPage filters client-side by the connected wallet (matched
 * against status.signerAddress).
 *
 * Each purchase is enriched with a `settlement` field sourced from the
 * verifier's `obol_x402_verifier_last_payment_success_seconds` gauge. The
 * join key is `(offer namespace, offer name, chain)` — offer name is parsed
 * from the endpoint URL (`/services/<name>/...`), offer namespace from the
 * matching ServiceOffer CR, and chain from `spec.payment.network`. External
 * x402 sellers (no matching ServiceOffer) get `settlement.lastSettlementUnix`
 * set to null so the drawer renders the empty state.
 */
export async function GET() {
  try {
    const client = new PurchasesClient();
    const purchases = await client.listPurchaseRequests();

    // Both the offers list and the Prometheus query are best-effort: if
    // either fails, we still return the purchases with settlement=null so
    // the drawer's empty state is preserved instead of surfacing an error.
    const [offers, lastSettlements] = await Promise.all([
      safeListOffers(),
      safeLastSettlements(),
    ]);

    const offersByName = indexOffersByName(offers);

    const enriched: PurchaseRequest[] = purchases.map(p => {
      const settlement = resolveSettlement(p, offersByName, lastSettlements);
      return { ...p, settlement };
    });

    const body: PurchaseListResponse = { purchases: enriched };
    return NextResponse.json(body);
  } catch (error) {
    console.error('[Purchases] Error:', error);
    return NextResponse.json(
      { error: 'Failed to list PurchaseRequests' },
      { status: 500 }
    );
  }
}

async function safeListOffers(): Promise<ServiceOffer[]> {
  try {
    const sell = new SellClient();
    return await sell.listServiceOffers();
  } catch (error) {
    console.warn(
      '[Purchases] ServiceOffer list failed, settlement disabled:',
      error
    );
    return [];
  }
}

async function safeLastSettlements(): Promise<Map<string, number>> {
  try {
    const prom = new PrometheusClient();
    return await prom.lastPaymentSuccessByOfferChain();
  } catch (error) {
    console.warn(
      '[Purchases] lastPaymentSuccessByOfferChain failed, settlement disabled:',
      error
    );
    return new Map();
  }
}

/**
 * Group ServiceOffers by name -> namespaces[]. Names are not globally unique
 * (different namespaces can host an offer with the same name), so callers
 * must resolve ambiguity by some other axis (e.g. chain match against the
 * Prometheus gauge).
 */
function indexOffersByName(offers: ServiceOffer[]): Map<string, string[]> {
  const idx = new Map<string, string[]>();
  for (const o of offers) {
    if (!o.name || !o.namespace) continue;
    const existing = idx.get(o.name) ?? [];
    existing.push(o.namespace);
    idx.set(o.name, existing);
  }
  return idx;
}

function resolveSettlement(
  p: PurchaseRequest,
  offersByName: Map<string, string[]>,
  lastSettlements: Map<string, number>
): { lastSettlementUnix: number | null } {
  const offerName = offerNameFromEndpoint(p.spec.endpoint);
  const chain = p.spec.payment.network;
  if (!offerName || !chain) return { lastSettlementUnix: null };

  const candidates = offersByName.get(offerName) ?? [];
  for (const ns of candidates) {
    const key = `${ns}/${offerName}/${chain}`;
    const ts = lastSettlements.get(key);
    if (ts !== undefined) return { lastSettlementUnix: ts };
  }
  return { lastSettlementUnix: null };
}
