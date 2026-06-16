import { NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';
import { SellClient } from '@/lib/services/sell';
import { fetchRawCatalog } from '@/lib/marketplace';
import { attachAgentListings } from '@/lib/services/agent-listing';
import { offerKey } from '@/lib/services/sell-catalog';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

export async function GET() {
  try {
    const client = new AgentClient();
    const sell = new SellClient();

    const [instances, catalog, liveOffers] = await Promise.all([
      client.listInstances(),
      fetchRawCatalog().catch(() => []),
      sell.listServiceOffers().catch(() => []),
    ]);

    const catalogKeys = new Set(
      catalog.map(e => offerKey(e.namespace ?? '', e.name ?? ''))
    );
    const enriched = attachAgentListings(instances, liveOffers, catalogKeys);

    return NextResponse.json({ instances: enriched });
  } catch (error) {
    console.error('[Agent List API] Error:', error);
    return NextResponse.json(
      { error: 'Failed to list agent instances' },
      { status: 500 }
    );
  }
}
