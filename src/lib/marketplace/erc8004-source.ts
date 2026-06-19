/**
 * Server-side source for the "Public ERC-8004" registry view of the
 * marketplace.
 *
 * Uses the same RegistryClient (8004scan) as the home AgentRegistry accordion.
 * Instead of a snapshot, we query page-by-page on demand — 3 parallel
 * requests per navigation (one per chain). The real totals come from 8004scan
 * metadata, so the count shown here always matches the home page.
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

const CHAIN_IDS: Record<number, ChainAlias> = {
  1: { alias: 'ethereum', caip2: 'eip155:1' },
  8453: { alias: 'base', caip2: 'eip155:8453' },
  84532: { alias: 'base-sepolia', caip2: 'eip155:84532' },
};

const REGISTRY_FANOUT_CHAINS = [1, 8453, 84532] as const;

export interface Erc8004PageResult {
  items: ServiceListItem[];
  /** Real total across all chains — from 8004scan pagination metadata. */
  total: number;
  /** Per-chain totals for display. */
  chainTotals: Record<number, number>;
  fetchedAt: string;
}

/**
 * Fetch one page of ERC-8004 agents.
 *
 * Fetches only the chains matching `networkFilter` (or all three if unset).
 * Returns real per-chain totals from 8004scan metadata — same numbers the
 * home AgentRegistry shows.
 */
export async function fetchErc8004Page(
  page: number,
  pageSize: number,
  search?: string,
  /** CAIP-2 values to restrict to, e.g. ['eip155:1']. Undefined = all chains. */
  networkFilter?: string[]
): Promise<Erc8004PageResult> {
  const client = new RegistryClient();

  const chainsToFetch = REGISTRY_FANOUT_CHAINS.filter(chainId => {
    if (!networkFilter?.length) return true;
    const caip2 = CHAIN_IDS[chainId]?.caip2;
    return caip2 !== undefined && networkFilter.includes(caip2);
  });

  const results = await Promise.all(
    chainsToFetch.map(chainId =>
      client
        .listAgents(pageSize, page, search, undefined, chainId)
        .catch(() => ({ agents: [] as Agent8004[], total: 0, hasMore: false }))
    )
  );

  // Start with zeros for all chains; fill in what we fetched.
  const chainTotals: Record<number, number> = {};
  REGISTRY_FANOUT_CHAINS.forEach(id => {
    chainTotals[id] = 0;
  });
  chainsToFetch.forEach((chainId, i) => {
    chainTotals[chainId] = results[i].total;
  });

  const total = results.reduce((sum, r) => sum + r.total, 0);
  const items = results.flatMap(r => r.agents).map(mapAgent8004ToItem);

  return { items, total, chainTotals, fetchedAt: new Date().toISOString() };
}

/**
 * Resolve a single ERC-8004 agent by its marketplace slug (erc8004/{chainId}/{tokenId}).
 * Used by the detail route — avoids loading a full snapshot.
 */
export async function fetchErc8004BySlug(
  slug: string
): Promise<ServiceListItem | null> {
  const parts = slug.split('/');
  if (parts.length !== 3 || parts[0] !== 'erc8004') return null;
  const chainId = parseInt(parts[1], 10);
  const tokenId = parseInt(parts[2], 10);
  if (!Number.isFinite(chainId) || !Number.isFinite(tokenId)) return null;

  const client = new RegistryClient();
  const result = await client
    .listAgents(20, 1, String(tokenId), undefined, chainId)
    .catch(() => ({ agents: [] as Agent8004[], total: 0, hasMore: false }));

  const agent = result.agents.find(
    a => a.token_id === String(tokenId) && a.chain_id === chainId
  );
  return agent ? mapAgent8004ToItem(agent) : null;
}

export function mapAgent8004ToItem(agent: Agent8004): ServiceListItem {
  const slug = `erc8004/${agent.chain_id}/${agent.token_id}`;
  const network = mapNetwork(agent.chain_id);
  const skills = dedupe([...(agent.categories ?? []), ...(agent.tags ?? [])]);

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
  if (agent.supported_protocols?.length) return 'agent';
  if (agent.services && Object.keys(agent.services).length > 0) return 'agent';
  return 'http';
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
