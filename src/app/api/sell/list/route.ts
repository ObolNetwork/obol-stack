import { NextResponse } from 'next/server';
import { SellClient } from '@/lib/services/sell';
import { AgentClient } from '@/lib/services/agents';
import { PrometheusClient } from '@/lib/services/prometheus';
import { linkOfferToAgent } from '@/lib/services/services-join';
import { fetchRawCatalog } from '@/lib/marketplace';
import {
  STACK_UNREACHABLE,
  isStackUnreachable,
} from '@/lib/marketplace/errors';
import { offerKey, serviceOfferFromCatalog } from '@/lib/services/sell-catalog';
import { splitPendingOffers } from '@/lib/services/agent-listing';
import type { ServiceOffer } from '@/types/sell';
import type { AgentInstance } from '@/types/agent';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/sell/list
 * Lists controller-published catalog services, enriched with the matching
 * ServiceOffer CR, local agent, and charged-request counts when available.
 *
 * The catalog is the row source. Raw ServiceOffer CRs are intentionally
 * best-effort enrichment only so the operator UI cannot drift from the
 * public storefront and partner-agent /api/services.json wire contract.
 */
export async function GET() {
  try {
    const sell = new SellClient();
    const agentsClient = new AgentClient();
    const prom = new PrometheusClient();

    // Two trust tiers:
    //  - catalog comes from the controller-authored /api/services.json and
    //    is the only source allowed to create rows.
    //  - ServiceOffer CRs, agents, and Prometheus are best-effort enrichments.
    const catalog = await fetchRawCatalog();

    const [liveOffers, agents] = await Promise.all([
      safeListServiceOffers(sell),
      safeListAgents(agentsClient),
    ]);
    const liveByKey = new Map(
      liveOffers.map(offer => [offerKey(offer.namespace, offer.name), offer])
    );

    const promResults = await Promise.allSettled([
      prom.chargedSales7dByOffer(),
      prom.chargedSalesByOfferAndChain(),
      prom.chargedRequests24hByOffer(),
      prom.lastSettlementByOffer(),
    ]);
    const [sales, perChain, reqs24h, lastSettlements] = promResults.map(
      (r, i) => {
        if (r.status === 'fulfilled') return r.value;
        const names = [
          'chargedSales7dByOffer',
          'chargedSalesByOfferAndChain',
          'chargedRequests24hByOffer',
          'lastSettlementByOffer',
        ];
        console.warn(
          `[Sell List] PromQL ${names[i]} failed, returning empty:`,
          r.reason
        );
        return new Map<string, unknown>();
      }
    ) as [
      Map<string, number>,
      Map<string, Array<{ chain: string; requests: number }>>,
      Map<string, number>,
      Map<string, number>,
    ];

    const offers = catalog
      .map(entry =>
        serviceOfferFromCatalog(
          entry,
          liveByKey.get(offerKey(entry.namespace ?? '', entry.name ?? ''))
        )
      )
      .filter((offer): offer is ServiceOffer => offer !== null);

    // Usage counts are pass-through from Prometheus only. If a query failed or
    // returned no series for this offer, the field stays null and the UI shows
    // "—". We never invent 0 — a real zero only appears when Prom returns it.
    const enrichUsage = (key: string) => {
      const charged = sales.get(key);
      const r24 = reqs24h.get(key);
      const last = lastSettlements.get(key);
      return {
        chargedRequests: charged === undefined ? null : charged,
        chargedByChain: perChain.get(key) ?? [],
        requests24h: r24 === undefined ? null : r24,
        lastSettlementUnix: last === undefined ? null : last,
      };
    };

    const enriched: ServiceOffer[] = offers.map(offer => {
      const key = `${offer.namespace}/${offer.name}`;
      return {
        ...offer,
        agentLink: linkOfferToAgent(offer, agents),
        usage: enrichUsage(key),
      };
    });

    const catalogKeys = new Set(
      catalog.map(e => offerKey(e.namespace ?? '', e.name ?? ''))
    );
    const pendingRaw = splitPendingOffers(liveOffers, catalogKeys);
    const pendingOffers: ServiceOffer[] = pendingRaw.map(offer => {
      const key = `${offer.namespace}/${offer.name}`;
      return {
        ...offer,
        agentLink: linkOfferToAgent(offer, agents),
        usage: enrichUsage(key),
      };
    });

    return NextResponse.json({ offers: enriched, pendingOffers });
  } catch (error) {
    console.error('[Sell List] Error:', error);
    if (isStackUnreachable(error)) {
      return NextResponse.json(
        { error: 'Obol Stack unreachable', code: STACK_UNREACHABLE },
        { status: 503 }
      );
    }
    return NextResponse.json(
      { error: 'Failed to list ServiceOffers' },
      { status: 500 }
    );
  }
}

async function safeListServiceOffers(
  client: SellClient
): Promise<ServiceOffer[]> {
  try {
    return await client.listServiceOffers();
  } catch (error) {
    console.warn(
      '[Sell List] ServiceOffer CR list failed, enrichment disabled:',
      error
    );
    return [];
  }
}

async function safeListAgents(client: AgentClient): Promise<AgentInstance[]> {
  try {
    return await client.listInstances();
  } catch (error) {
    console.warn('[Sell List] agent list failed, links disabled:', error);
    return [];
  }
}
