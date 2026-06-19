import { NextResponse, type NextRequest } from 'next/server';
import { rejectCrossOriginCredentialRead } from '@/lib/middleware/origin-check';
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
export async function GET(request: NextRequest) {
  const csrfReject = rejectCrossOriginCredentialRead(request);
  if (csrfReject) return csrfReject;

  try {
    const client = new AgentClient();
    // Fetch instances + ALL wallet-metadata ConfigMaps across every namespace.
    // We include wallets from ALL namespaces (not just those with a running
    // instance) so that master-agent and sub-agent wallets are all captured.
    const [instances, walletByNs] = await Promise.all([
      client.listInstances(),
      client.listAllWalletMetadata(),
    ]);

    // Build per-agent breakdown (for UI display) keyed on known instances.
    const perAgent: OperatorWallet[] = instances.map(instance => {
      const meta = walletByNs.get(instance.namespace);
      const addresses = (meta?.addresses ?? [])
        .map(w => (typeof w === 'string' ? w : w.address))
        .filter((a): a is string => typeof a === 'string' && a.length > 0);
      return { instanceId: instance.id, runtime: instance.runtime, addresses };
    });

    // Flat address set: union of every wallet-metadata ConfigMap across ALL
    // namespaces — this catches master-agent and any namespace without a
    // running AgentInstance that still owns listings.
    const allAddresses = Array.from(
      new Set(
        Array.from(walletByNs.values()).flatMap(meta =>
          (meta.addresses ?? [])
            .map(w => (typeof w === 'string' ? w : w.address))
            .filter((a): a is string => typeof a === 'string' && a.length > 0)
            .map(a => a.toLowerCase())
        )
      )
    );

    const body: OperatorWalletsResponse = {
      agents: perAgent,
      addresses: allAddresses,
    };
    return NextResponse.json(body);
  } catch (error) {
    console.error('[OperatorWallets] Error:', error);
    return NextResponse.json(
      { agents: [], addresses: [], error: 'Failed to list agent wallets' },
      { status: 500 }
    );
  }
}
