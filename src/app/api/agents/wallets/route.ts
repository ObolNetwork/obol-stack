import { NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';
import type { AgentInstance } from '@/types/agent';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

interface OperatorWallet {
  instanceId: string;
  runtime: AgentInstance['runtime'];
  addresses: string[];
}

interface OperatorWalletsResponse {
  /** Per-agent breakdown for surfacing in the WalletStrip. */
  agents: OperatorWallet[];
  /** Flat lower-cased union — used by client-side filters
   *  (MyListings filters offers by payTo, MyPurchases by signerAddress). */
  addresses: string[];
}

/**
 * GET /api/agents/wallets
 *
 * Aggregates every wallet address controlled by every agent on this stack.
 * The marketplace pages treat the operator's "owned" set as
 * (agent wallets) ∪ (connected browser wallet) — see useOperatorWallets.
 */
export async function GET() {
  try {
    const client = new AgentClient();
    const instances = await client.listInstances();

    const perAgent = await Promise.all(
      instances.map(async (instance): Promise<OperatorWallet | null> => {
        try {
          const meta = await client.getWalletMetadata(instance);
          return {
            instanceId: instance.id,
            runtime: instance.runtime,
            addresses: (meta.addresses ?? [])
              .map(w => (typeof w === 'string' ? w : w.address))
              .filter(
                (a): a is string => typeof a === 'string' && a.length > 0
              ),
          };
        } catch (error) {
          console.warn(
            `[OperatorWallets] ${instance.runtime}/${instance.id} failed:`,
            error
          );
          return null;
        }
      })
    );

    const agents = perAgent.filter((x): x is OperatorWallet => x !== null);
    const addresses = Array.from(
      new Set(agents.flatMap(a => a.addresses.map(addr => addr.toLowerCase())))
    );

    const body: OperatorWalletsResponse = { agents, addresses };
    return NextResponse.json(body);
  } catch (error) {
    console.error('[OperatorWallets] Error:', error);
    return NextResponse.json(
      { agents: [], addresses: [], error: 'Failed to list agent wallets' },
      { status: 500 }
    );
  }
}
