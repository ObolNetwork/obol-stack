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
import { classifyFetchFailure } from './errors.ts';
import type { ServiceOfferType } from '@/types/sell';

export const CATALOG_SERVICE =
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
    transferMethod?: string;
    eip712Domain?: {
      name?: string;
      version?: string;
    };
  };
  description?: string;
  isDemo?: boolean;
  registrationPending?: boolean;
  drainEndsAt?: string;
}

export interface CatalogSnapshot {
  items: ServiceListItem[];
  fetchedAt: string;
}

export async function fetchCatalog(
  url: string = CATALOG_SERVICE
): Promise<CatalogSnapshot> {
  const body = await fetchRawCatalog(url);
  return {
    items: mapCatalog(body),
    fetchedAt: new Date().toISOString(),
  };
}

export async function fetchRawCatalog(
  url: string = CATALOG_SERVICE
): Promise<RawCatalogEntry[]> {
  let res: Response;
  try {
    res = await fetch(url, {
      headers: { accept: 'application/json' },
      // Next.js fetch cache: 30s ISR window. Each route handler caps it
      // lower if needed via its own revalidate export.
      next: { revalidate: 30 },
    });
  } catch (e) {
    if (isDefaultCatalogMissing(url, e)) return [];
    // Network failure (DNS, ECONNREFUSED, abort). Classify as unreachable
    // so the API route can flip a typed error to the UI.
    throw classifyFetchFailure(e, 0);
  }
  if (!res.ok) {
    if (res.status === 404 && isDefaultCatalogService(url)) return [];
    throw classifyFetchFailure(
      new Error(`catalog fetch ${url} → HTTP ${res.status}`),
      res.status
    );
  }
  const body = (await res.json()) as unknown;
  return Array.isArray(body) ? (body as RawCatalogEntry[]) : [];
}

function isDefaultCatalogService(url: string): boolean {
  return url === CATALOG_SERVICE && url.includes('obol-skill-md.x402.');
}

function isDefaultCatalogMissing(url: string, error: unknown): boolean {
  if (!isDefaultCatalogService(url)) return false;
  const text = errorText(error).toLowerCase();
  return (
    text.includes('obol-skill-md') ||
    text.includes('enotfound') ||
    text.includes('name_not_resolved')
  );
}

function errorText(error: unknown): string {
  const seen = new Set<unknown>();
  const parts: string[] = [];
  let current: unknown = error;
  while (current && !seen.has(current)) {
    seen.add(current);
    if (current instanceof Error) {
      parts.push(current.message);
      current = current.cause;
      continue;
    }
    if (typeof current === 'object') {
      const e = current as Record<string, unknown>;
      for (const key of ['message', 'code', 'hostname', 'host']) {
        const value = e[key];
        if (typeof value === 'string') parts.push(value);
      }
      current = e.cause;
      continue;
    }
    parts.push(String(current));
    break;
  }
  return parts.join(' ');
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
    price: normalizePriceDisplay(entry.priceRaw ?? entry.price),
    priceUnit: normalizePriceUnit(entry.priceUnit),
    priceAtomicUnits: (entry.priceAtomicUnits ?? '').trim(),
    asset,
    network,
    endpoint: (entry.endpoint ?? '').trim(),
    payTo: (entry.payTo ?? '').trim(),
    status: deriveStatus(entry),
    isDemo: entry.isDemo === true,
    drainEndsAt:
      typeof entry.drainEndsAt === 'string'
        ? entry.drainEndsAt.trim()
        : undefined,
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
      return 'agent-turn';
    case 'mtok':
    case 'perMTok':
    case 'perMtok':
      return 'mtok';
    case 'hour':
    case 'perHour':
      return 'hour';
    case 'epoch':
      return 'epoch';
    case 'request':
    case 'perRequest':
    default:
      return 'request';
  }
}

function normalizePriceDisplay(value: string | undefined): string {
  const trimmed = (value ?? '').trim();
  if (!trimmed) return '';
  const numericPrefix = /^(\d+(?:\.\d+)?)/.exec(trimmed);
  return numericPrefix?.[1] ?? trimmed;
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
    transferMethod: (raw?.transferMethod ?? '').trim() || undefined,
    eip712Domain:
      raw?.eip712Domain && (raw.eip712Domain.name || raw.eip712Domain.version)
        ? {
            name: (raw.eip712Domain.name ?? '').trim() || undefined,
            version: (raw.eip712Domain.version ?? '').trim() || undefined,
          }
        : undefined,
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
