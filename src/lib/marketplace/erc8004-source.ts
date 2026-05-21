/**
 * Server-side source for the "Public ERC-8004" registry view of the
 * marketplace.
 *
 * Delegates to the existing 8004scan-backed RegistryClient — the same
 * indexer the AgentRegistry organism on the home dashboard uses — so the
 * two surfaces share one fetch path. This module's job is the mapping
 * from `Agent8004` (8004scan's shape) onto the marketplace's
 * ServiceListItem so the same card UI handles both data sources.
 *
 * Why an indexer instead of direct `eth_getLogs`: 8004scan already
 * aggregates registrations across chains, exposes search + pagination,
 * and resolves agent metadata, so a marketplace browse is one HTTP call
 * instead of N chain scans + N metadata fetches.
 */

import { createHash } from 'node:crypto';

import { RegistryClient } from '../services/registry.ts';
import type { Agent8004 } from '@/types/registry';

import type {
  MarketplaceAsset,
  MarketplaceNetwork,
  MarketplacePriceUnit,
  MarketplaceStatus,
  ServiceListItem,
} from './types';
import type { ServiceOfferType } from '@/types/sell';

interface ChainAlias {
  alias: string;
  caip2: string;
}

// CHAIN_IDS mirrors the obol-stack ERC-8004 network config + 8004scan
// chain support. Anything not in this map renders as "eip155:<id>".
const CHAIN_IDS: Record<number, ChainAlias> = {
  1: { alias: 'ethereum', caip2: 'eip155:1' },
  8453: { alias: 'base', caip2: 'eip155:8453' },
  84532: { alias: 'base-sepolia', caip2: 'eip155:84532' },
};

// Server-side fan-out: we ask 8004scan for the same window of entries
// on each supported chain. 100 entries per chain is enough for v1 and
// keeps the request count low.
const REGISTRY_FANOUT_CHAINS = [1, 8453, 84532] as const;
const REGISTRY_PAGE_SIZE = 100;

export interface Erc8004Snapshot {
  items: ServiceListItem[];
  fetchedAt: string;
}

/**
 * Fetch all ERC-8004 registrations 8004scan knows about across the
 * configured chains, mapped onto ServiceListItem.
 *
 * Per-chain failures are caught individually so one chain being down
 * doesn't sink the whole list — same behaviour as the previous chain-
 * scan implementation, just at the indexer-fetch level.
 */
export async function fetchErc8004Registry(): Promise<Erc8004Snapshot> {
  const client = new RegistryClient();
  const perChain = await Promise.all(
    REGISTRY_FANOUT_CHAINS.map(chainId =>
      client
        .listAgents(REGISTRY_PAGE_SIZE, 1, undefined, undefined, chainId)
        .then(r => r.agents)
        .catch(() => [] as Agent8004[])
    )
  );
  const items = perChain.flat().map(mapAgent8004ToItem);
  return {
    items,
    fetchedAt: new Date().toISOString(),
  };
}

/**
 * Pure mapping helper — given one Agent8004 entry from 8004scan,
 * returns the ServiceListItem the marketplace cards/table render.
 *
 * Exported for unit tests so the mapping can be exercised without
 * hitting the network.
 */
export function mapAgent8004ToItem(agent: Agent8004): ServiceListItem {
  const slug = `erc8004/${agent.chain_id}/${agent.token_id}`;
  const network = mapNetwork(agent.chain_id);
  const skills = dedupe([...(agent.categories ?? []), ...(agent.tags ?? [])]);

  // 8004scan doesn't expose x402 pricing/asset directly on the agent
  // payload, so we surface a placeholder. A reviewer who needs price
  // can probe the agent's endpoint with the buy-x402 sidecar.
  const asset: MarketplaceAsset = {
    symbol: 'USDC',
    address: '',
    decimals: 6,
  };

  const status: MarketplaceStatus =
    agent.health_status === 'degraded' ? 'degraded' : 'live';

  const type: ServiceOfferType = deriveType(agent);

  return {
    slug,
    name: agent.name?.trim() || `agent-${agent.token_id}`,
    namespace: `erc8004:${network.alias}`,
    type,
    modelOrRuntime: deriveModelOrRuntime(agent),
    skills,
    price: '',
    priceUnit: 'agent-turn' satisfies MarketplacePriceUnit,
    priceAtomicUnits: '',
    asset,
    network,
    endpoint: deriveEndpoint(agent),
    payTo: agent.owner_address ?? '',
    status,
    isDemo: agent.is_testnet === true,
    description:
      agent.description?.trim() ||
      `On-chain ERC-8004 agent #${agent.token_id} on ${network.alias}.`,
    bannerSeed: bannerSeed(slug),
  };
}

function mapNetwork(chainId: number): MarketplaceNetwork {
  const known = CHAIN_IDS[chainId];
  return {
    caip2: known?.caip2 ?? `eip155:${chainId}`,
    chainId,
    alias: known?.alias ?? `eip155:${chainId}`,
  };
}

function deriveType(agent: Agent8004): ServiceOfferType {
  // Agent registrations declare protocols (e.g. MCP) and services. If
  // there's a "model" in the services map we'd flag inference, but
  // 8004scan's shape doesn't separate that — call everything "agent"
  // by default and let `http` apply when no services are listed.
  const hasServices =
    !!agent.services && Object.keys(agent.services).length > 0;
  return hasServices ? 'agent' : 'http';
}

function deriveModelOrRuntime(agent: Agent8004): string {
  if (agent.supported_protocols?.length) {
    return agent.supported_protocols.join(', ');
  }
  return '—';
}

function deriveEndpoint(agent: Agent8004): string {
  if (!agent.services) return '';
  for (const entry of Object.values(agent.services)) {
    const endpoint = entry?.endpoint?.trim();
    if (endpoint) return endpoint;
  }
  return '';
}

function dedupe(values: readonly string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const v of values) {
    const t = v?.trim();
    if (!t || seen.has(t)) continue;
    seen.add(t);
    out.push(t);
  }
  return out;
}

function bannerSeed(slug: string): string {
  return createHash('sha256').update(slug).digest('hex').slice(0, 12);
}
