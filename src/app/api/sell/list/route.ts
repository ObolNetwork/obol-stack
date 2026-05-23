import { NextResponse } from 'next/server';
import { SellClient } from '@/lib/services/sell';
import { AgentClient } from '@/lib/services/agents';
import { PrometheusClient } from '@/lib/services/prometheus';
import { linkOfferToAgent } from '@/lib/services/services-join';
import type { ServiceOffer } from '@/types/sell';
import type { AgentInstance } from '@/types/agent';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/sell/list
 * Lists all ServiceOffer CRs, enriched with the matching agent (when the
 * upstream namespace is a hermes- or openclaw- agent) and charged-request
 * counts scraped from Prometheus.
 */
export async function GET() {
  try {
    const sell = new SellClient();
    const agentsClient = new AgentClient();
    const prom = new PrometheusClient();

    const [offers, agents, sales, perChain, reqs24h, lastSettlements] =
      await Promise.all([
        sell.listServiceOffers(),
        safeListAgents(agentsClient),
        prom.chargedSalesByOffer(),
        prom.chargedSalesByOfferAndChain(),
        prom.chargedRequests24hByOffer(),
        prom.lastSettlementByOffer(),
      ]);

    const enriched: ServiceOffer[] = offers.map(offer => {
      const key = `${offer.namespace}/${offer.name}`;
      const charged = sales.get(key);
      const chainBreakdown = perChain.get(key) ?? [];
      const r24 = reqs24h.get(key);
      const last = lastSettlements.get(key);
      return {
        ...offer,
        agentLink: linkOfferToAgent(offer, agents),
        usage: {
          chargedRequests: charged === undefined ? null : charged,
          chargedByChain: chainBreakdown,
          requests24h: r24 === undefined ? null : r24,
          lastSettlementUnix: last === undefined ? null : last,
        },
      };
    });

    return NextResponse.json({ offers: enriched });
  } catch (error) {
    console.error('[Sell List] Error:', error);
    return NextResponse.json(
      { error: 'Failed to list ServiceOffers' },
      { status: 500 }
    );
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
