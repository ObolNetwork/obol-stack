/**
 * Fetches the controller-published /api/services.json catalog and maps the
 * raw ServiceCatalogEntry shape into a UI-friendly ServiceListItem[].
 *
 * Source of truth is internal/serviceoffercontroller/render.go +
 * internal/schemas/service_catalog.go in obol-stack. The catalog is served by
 * a busybox httpd backed by ConfigMap `obol-skill-md` in the `x402`
 * namespace on port 8080 — we hit it directly to skip the Traefik hop.
 */

import { createHash } from 'node:crypto';
import type {
  MarketplaceAsset,
  MarketplaceNetwork,
  MarketplacePriceUnit,
  MarketplaceStatus,
  ServiceListItem,
} from './types';
import type { ServiceOfferType } from '@/types/sell';

const CATALOG_SERVICE =
  process.env.MARKETPLACE_CATALOG_URL ||
  'http://obol-skill-md.x402.svc.cluster.local:8080/api/services.json';

/**
 * Raw ServiceCatalogEntry — mirrors internal/schemas/service-catalog.schema.json.
 * Kept loose on purpose so a schema rev that adds optional fields doesn't
 * break the fetcher.
 */
export interface RawCatalogEntry {
  name?: string;
  namespace?: string;
  type?: string;
  model?: string;
  modelRuntime?: string;
  agentRuntime?: string;
  agentModel?: string;
  agentSkills?: string[];
  skills?: string[];
  endpoint?: string;
  price?: string;
  priceRaw?: string;
  priceUnit?: string;
  priceAtomicUnits?: string;
  payTo?: string;
  network?: string;
  caip2Network?: string;
  chainId?: number;
  asset?: {
    symbol?: string;
    address?: string;
    decimals?: number;
  };
  description?: string;
  isDemo?: boolean;
  registrationPending?: boolean;
}

export interface CatalogSnapshot {
  items: ServiceListItem[];
  fetchedAt: string;
}

export async function fetchCatalog(
  url: string = CATALOG_SERVICE
): Promise<CatalogSnapshot> {
  const res = await fetch(url, {
    headers: { accept: 'application/json' },
    // Next.js fetch cache: 30s ISR window. Each route handler caps it lower
    // if needed via its own revalidate export.
    next: { revalidate: 30 },
  });
  if (!res.ok) {
    throw new Error(`catalog fetch ${url} → HTTP ${res.status}`);
  }
  const body = (await res.json()) as unknown;
  return {
    items: mapCatalog(body),
    fetchedAt: new Date().toISOString(),
  };
}

/**
 * Pure transform — exported so it can be unit-tested without hitting the
 * cluster.
 */
export function mapCatalog(raw: unknown): ServiceListItem[] {
  if (!Array.isArray(raw)) return [];
  return raw
    .map(entry => mapEntry(entry as RawCatalogEntry))
    .filter((item): item is ServiceListItem => item !== null);
}

function mapEntry(entry: RawCatalogEntry): ServiceListItem | null {
  const name = (entry.name || '').trim();
  const namespace = (entry.namespace || '').trim();
  if (!name || !namespace) return null;

  const type = normalizeType(entry.type);
  const asset = mapAsset(entry.asset);
  const network = mapNetwork(entry);
  const slug = `${namespace}/${name}`;

  return {
    slug,
    name,
    namespace,
    type,
    modelOrRuntime: deriveModelOrRuntime(type, entry),
    skills: dedupeStrings([
      ...(entry.skills ?? []),
      ...(entry.agentSkills ?? []),
    ]),
    price: (entry.price ?? entry.priceRaw ?? '').trim(),
    priceUnit: normalizePriceUnit(entry.priceUnit),
    priceAtomicUnits: (entry.priceAtomicUnits ?? '').trim(),
    asset,
    network,
    endpoint: (entry.endpoint ?? '').trim(),
    status: deriveStatus(entry),
    isDemo: entry.isDemo === true,
    description: (entry.description ?? '').trim(),
    bannerSeed: bannerSeed(slug),
  };
}

function normalizeType(value: string | undefined): ServiceOfferType {
  switch (value) {
    case 'inference':
    case 'fine-tuning':
    case 'agent':
    case 'http':
      return value;
    default:
      return 'http';
  }
}

function normalizePriceUnit(value: string | undefined): MarketplacePriceUnit {
  switch (value) {
    case 'agent-turn':
    case 'mtok':
    case 'hour':
    case 'epoch':
      return value;
    case 'request':
    default:
      return 'request';
  }
}

function deriveModelOrRuntime(
  type: ServiceOfferType,
  entry: RawCatalogEntry
): string {
  if (type === 'agent') {
    const runtime = (entry.agentRuntime || 'hermes').trim();
    const model = (entry.agentModel || entry.model || '').trim();
    return model ? `${runtime} · ${model}` : runtime;
  }
  return (entry.model || entry.modelRuntime || '').trim();
}

function mapAsset(raw: RawCatalogEntry['asset']): MarketplaceAsset {
  return {
    symbol: (raw?.symbol || 'USDC').toUpperCase(),
    address: (raw?.address ?? '').trim(),
    decimals: typeof raw?.decimals === 'number' ? raw.decimals : 6,
  };
}

function mapNetwork(entry: RawCatalogEntry): MarketplaceNetwork {
  const caip2 = (entry.caip2Network || entry.network || '').trim();
  const chainId =
    typeof entry.chainId === 'number'
      ? entry.chainId
      : parseChainIdFromCaip2(caip2);
  return {
    caip2: caip2 || 'eip155:0',
    chainId,
    alias: (entry.network || caip2 || '').trim(),
  };
}

function parseChainIdFromCaip2(caip2: string): number {
  const m = /^eip155:(\d+)$/.exec(caip2);
  return m ? Number(m[1]) : 0;
}

function deriveStatus(entry: RawCatalogEntry): MarketplaceStatus {
  if (entry.registrationPending === true) return 'pending-registration';
  return 'live';
}

function dedupeStrings(values: readonly string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const v of values) {
    const trimmed = v?.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    out.push(trimmed);
  }
  return out;
}

function bannerSeed(slug: string): string {
  return createHash('sha256').update(slug).digest('hex').slice(0, 12);
}
