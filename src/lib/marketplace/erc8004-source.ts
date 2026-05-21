/**
 * Server-side source for the "Public ERC-8004" registry view.
 *
 * Reads the `Registered(uint256 indexed agentId, string agentURI,
 * address indexed owner)` event log from the canonical Identity Registry
 * contracts on each supported chain via eRPC, then fetches each agent's
 * metadata URI to surface model / runtime / pricing data.
 *
 * Identity Registry addresses (from obol-stack `internal/erc8004/abi.go`):
 *   Base Sepolia    0x8004A818BFB912233c491871b3d84c89A494BD9e
 *   Ethereum + Base 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432  (CREATE2)
 *
 * Map output is a ServiceListItem so the listing reuses the same UI
 * components as the obol catalog — the registry switch is purely a
 * data-source toggle, not a UI fork.
 */

import { createHash } from 'node:crypto';
import {
  createPublicClient,
  http,
  parseAbiItem,
  type Address,
  type Log,
} from 'viem';
import { mainnet, base, baseSepolia } from 'viem/chains';

import type {
  MarketplaceAsset,
  MarketplaceNetwork,
  MarketplacePriceUnit,
  MarketplaceStatus,
  ServiceListItem,
} from './types';
import type { ServiceOfferType } from '@/types/sell';

const ERPC_BASE =
  process.env.MARKETPLACE_ERPC_BASE || 'http://erpc.erpc.svc.cluster.local/rpc';

interface ChainConfig {
  alias: string;
  chainId: number;
  registry: Address;
  caip2: string;
  viemChain: typeof mainnet | typeof base | typeof baseSepolia;
  /** Block to start scanning from. Earliest unless the registry was deployed late. */
  fromBlock: bigint;
}

const CHAINS: ChainConfig[] = [
  {
    alias: 'mainnet',
    chainId: 1,
    registry: '0x8004A169FB4a3325136EB29fA0ceB6D2e539a432',
    caip2: 'eip155:1',
    viemChain: mainnet,
    fromBlock: 0n,
  },
  {
    alias: 'base',
    chainId: 8453,
    registry: '0x8004A169FB4a3325136EB29fA0ceB6D2e539a432',
    caip2: 'eip155:8453',
    viemChain: base,
    fromBlock: 0n,
  },
  {
    alias: 'base-sepolia',
    chainId: 84532,
    registry: '0x8004A818BFB912233c491871b3d84c89A494BD9e',
    caip2: 'eip155:84532',
    viemChain: baseSepolia,
    fromBlock: 0n,
  },
];

const REGISTERED_EVENT = parseAbiItem(
  'event Registered(uint256 indexed agentId, string agentURI, address indexed owner)'
);

const METADATA_FETCH_TIMEOUT_MS = 5_000;
const METADATA_FETCH_CONCURRENCY = 10;

/**
 * The well-known metadata document the controller publishes. Loose-typed
 * because external operators might publish slightly different shapes; we
 * tolerate missing fields.
 */
interface RegistrationDoc {
  name?: string;
  description?: string;
  endpoint?: string;
  registration?: {
    metadata?: {
      model?: string;
      runtime?: string;
      pricingUnit?: string;
      x402Price?: string;
      x402Asset?: string;
      x402Network?: string;
    };
    skills?: string[];
  };
  services?: Array<{ name?: string; endpoint?: string }>;
}

interface RegisteredAgent {
  chain: ChainConfig;
  agentId: bigint;
  agentURI: string;
  owner: Address;
}

export interface Erc8004Snapshot {
  items: ServiceListItem[];
  fetchedAt: string;
}

/**
 * Fetch + map the full ERC-8004 registry across all configured chains.
 * Hits each chain's Identity Registry, decodes every `Registered` event,
 * fetches each metadata URI in parallel (bounded concurrency + timeout),
 * and returns one ServiceListItem per agent.
 */
export async function fetchErc8004Registry(): Promise<Erc8004Snapshot> {
  // Each chain queried independently — one chain being slow or offline
  // shouldn't sink the whole list. Failures become empty arrays.
  const perChain = await Promise.all(
    CHAINS.map(c => safeReadAgents(c).catch(() => [] as RegisteredAgent[]))
  );
  const agents = perChain.flat();

  // Bounded-concurrency metadata fetch. We use a small worker pool so a
  // few slow well-known servers can't head-of-line block the whole list.
  const items = await mapWithLimit(
    agents,
    METADATA_FETCH_CONCURRENCY,
    async agent => mapAgentToItem(agent)
  );

  return {
    items: items.filter((it): it is ServiceListItem => it !== null),
    fetchedAt: new Date().toISOString(),
  };
}

async function safeReadAgents(chain: ChainConfig): Promise<RegisteredAgent[]> {
  const client = createPublicClient({
    chain: chain.viemChain,
    transport: http(`${ERPC_BASE}/${chain.alias}`),
  });
  // viem's getLogs handles topic encoding from the parsed event ABI.
  const logs = await client.getLogs({
    address: chain.registry,
    event: REGISTERED_EVENT,
    fromBlock: chain.fromBlock,
    toBlock: 'latest',
  });
  return logs.map(log => decodeRegistered(log, chain));
}

function decodeRegistered(
  log: Log<bigint, number, false, typeof REGISTERED_EVENT>,
  chain: ChainConfig
): RegisteredAgent {
  // viem decodes indexed + non-indexed args into log.args for typed events.
  const args = (log as unknown as { args: Record<string, unknown> }).args;
  return {
    chain,
    agentId: args.agentId as bigint,
    agentURI: String(args.agentURI ?? ''),
    owner: (args.owner ??
      '0x0000000000000000000000000000000000000000') as Address,
  };
}

async function mapAgentToItem(
  agent: RegisteredAgent
): Promise<ServiceListItem | null> {
  if (!agent.agentURI) return null;
  const doc = await fetchMetadata(agent.agentURI);
  return buildServiceListItem(agent, doc);
}

async function fetchMetadata(uri: string): Promise<RegistrationDoc | null> {
  try {
    const ac = new AbortController();
    const timer = setTimeout(() => ac.abort(), METADATA_FETCH_TIMEOUT_MS);
    const res = await fetch(uri, {
      headers: { accept: 'application/json' },
      signal: ac.signal,
    });
    clearTimeout(timer);
    if (!res.ok) return null;
    return (await res.json()) as RegistrationDoc;
  } catch {
    // Network failure / abort / bad JSON — fall back to a stub entry so
    // the agent is still discoverable but flagged as "metadata unreachable".
    return null;
  }
}

/**
 * Build a ServiceListItem for one on-chain agent. When the metadata
 * document is available, fields are populated from it; otherwise we
 * still surface a row with what we know from the event log + chain.
 */
function buildServiceListItem(
  agent: RegisteredAgent,
  doc: RegistrationDoc | null
): ServiceListItem {
  const metadata = doc?.registration?.metadata ?? {};
  const slug = `erc8004/${agent.chain.chainId}/${agent.agentId.toString()}`;
  const name = (doc?.name || `agent-${agent.agentId.toString()}`).trim();
  const namespace = `erc8004:${agent.chain.alias}`;

  const type: ServiceOfferType =
    metadata.runtime || metadata.model ? 'agent' : 'http';

  const modelOrRuntime = (() => {
    const runtime = (metadata.runtime || '').trim();
    const model = (metadata.model || '').trim();
    if (runtime && model) return `${runtime} · ${model}`;
    return model || runtime || '—';
  })();

  const skills = dedupe(doc?.registration?.skills ?? []);

  const asset: MarketplaceAsset = {
    symbol: (metadata.x402Asset || 'USDC').toUpperCase(),
    address: '',
    decimals: 6,
  };

  const network: MarketplaceNetwork = {
    caip2: agent.chain.caip2,
    chainId: agent.chain.chainId,
    alias: metadata.x402Network || agent.chain.alias,
  };

  const status: MarketplaceStatus = doc ? 'live' : 'pending-registration';

  const priceUnit = normalizePriceUnit(metadata.pricingUnit);

  return {
    slug,
    name,
    namespace,
    type,
    modelOrRuntime,
    skills,
    price: (metadata.x402Price ?? '').trim(),
    priceUnit,
    priceAtomicUnits: '',
    asset,
    network,
    endpoint: (doc?.endpoint || doc?.services?.[0]?.endpoint || '').trim(),
    payTo: agent.owner,
    status,
    isDemo: false,
    description: (
      doc?.description ||
      `On-chain ERC-8004 agent #${agent.agentId.toString()} on ${agent.chain.alias}.`
    ).trim(),
    bannerSeed: bannerSeed(slug),
  };
}

function normalizePriceUnit(value: string | undefined): MarketplacePriceUnit {
  switch (value) {
    case 'agent-turn':
    case 'mtok':
    case 'hour':
    case 'epoch':
    case 'request':
      return value;
    default:
      return 'agent-turn';
  }
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

/**
 * Run `fn` over `items` with a fixed worker pool. Returns results in the
 * same order as input. Used so a few slow metadata fetches don't
 * head-of-line block the rest of the list.
 */
async function mapWithLimit<T, R>(
  items: readonly T[],
  limit: number,
  fn: (item: T, index: number) => Promise<R>
): Promise<R[]> {
  const results: R[] = new Array(items.length);
  let nextIndex = 0;
  const workers = Array.from(
    { length: Math.min(limit, items.length) },
    async () => {
      while (true) {
        const i = nextIndex++;
        if (i >= items.length) return;
        results[i] = await fn(items[i], i);
      }
    }
  );
  await Promise.all(workers);
  return results;
}

/**
 * Pure mapping helper exported for unit tests — given a decoded event
 * and a (possibly-missing) metadata document, returns the
 * ServiceListItem we'd surface. No network I/O.
 */
export function mapAgentEventToItem(
  args: {
    chainId: number;
    chainAlias: string;
    chainCaip2: string;
    agentId: bigint;
    agentURI: string;
    owner: Address;
  },
  doc: RegistrationDoc | null
): ServiceListItem {
  const chain: ChainConfig = {
    alias: args.chainAlias,
    chainId: args.chainId,
    registry: '0x0000000000000000000000000000000000000000',
    caip2: args.chainCaip2,
    viemChain: mainnet,
    fromBlock: 0n,
  };
  return buildServiceListItem(
    {
      chain,
      agentId: args.agentId,
      agentURI: args.agentURI,
      owner: args.owner,
    },
    doc
  );
}
