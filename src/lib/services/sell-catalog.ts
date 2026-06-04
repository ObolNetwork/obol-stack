import type { RawCatalogEntry } from '../marketplace/catalog.ts';
import type {
  ServiceOffer,
  ServiceOfferAsset,
  ServiceOfferPrice,
  ServiceOfferType,
} from '../../types/sell.ts';

export function offerKey(namespace: string, name: string): string {
  return `${namespace}/${name}`;
}

export function serviceOfferFromCatalog(
  entry: RawCatalogEntry,
  live?: ServiceOffer
): ServiceOffer | null {
  const name = (entry.name ?? '').trim();
  const namespace = (entry.namespace ?? '').trim();
  if (!name || !namespace) return null;

  const type = normalizeType(entry.type);
  const skills = dedupeStrings([
    ...(entry.skills ?? []),
    ...(entry.agentSkills ?? []),
    ...(live?.registration?.skills ?? []),
    ...(live?.agentResolution?.skills ?? []),
  ]);
  const description =
    (entry.description ?? '').trim() || live?.registration?.description;
  const modelName =
    (entry.model ?? '').trim() ||
    (entry.agentModel ?? '').trim() ||
    live?.model?.name ||
    live?.agentResolution?.model;
  const modelRuntime =
    (entry.modelRuntime ?? '').trim() ||
    (entry.agentRuntime ?? '').trim() ||
    live?.model?.runtime ||
    live?.agentResolution?.runtime;
  const priceRaw = normalizePriceDisplay(entry.priceRaw ?? entry.price);

  return {
    name,
    namespace,
    type,
    status: live?.status ?? 'Ready',
    priceRaw: priceRaw || undefined,
    priceUnit: (entry.priceUnit ?? '').trim() || undefined,
    priceAtomicUnits: (entry.priceAtomicUnits ?? '').trim() || undefined,
    caip2Network: (entry.caip2Network ?? '').trim() || undefined,
    chainId: typeof entry.chainId === 'number' ? entry.chainId : undefined,
    isDemo: entry.isDemo === true,
    registrationPending: entry.registrationPending === true,
    drainEndsAt:
      typeof entry.drainEndsAt === 'string'
        ? entry.drainEndsAt.trim() || undefined
        : undefined,
    provenance: live?.provenance,
    paused: live?.paused,
    createdAt: live?.createdAt,
    endpoint: (entry.endpoint ?? '').trim() || live?.endpoint,
    conditions: live?.conditions ?? [],
    agent: live?.agent,
    model:
      modelName || modelRuntime
        ? { name: modelName, runtime: modelRuntime }
        : live?.model,
    upstream: live?.upstream,
    payment: {
      scheme: live?.payment?.scheme ?? 'exact',
      network: (entry.network ?? '').trim() || live?.payment?.network,
      payTo: (entry.payTo ?? '').trim() || live?.payment?.payTo,
      asset: catalogAsset(entry.asset, live?.payment?.asset),
      price: catalogPrice(entry.priceUnit, priceRaw, live?.payment?.price),
    },
    registration: {
      ...live?.registration,
      name: live?.registration?.name ?? name,
      description,
      skills,
    },
    agentLink: null,
    agentResolution: live?.agentResolution ?? null,
    usage: { chargedRequests: null },
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

function catalogAsset(
  raw: RawCatalogEntry['asset'],
  live?: ServiceOfferAsset
): ServiceOfferAsset | undefined {
  if (!raw && !live) return undefined;
  return {
    symbol: (raw?.symbol ?? live?.symbol ?? '').trim() || undefined,
    decimals: typeof raw?.decimals === 'number' ? raw.decimals : live?.decimals,
    address: (raw?.address ?? live?.address ?? '').trim() || undefined,
    transferMethod:
      (raw?.transferMethod ?? live?.transferMethod ?? '').trim() || undefined,
    eip712Domain:
      raw?.eip712Domain || live?.eip712Domain
        ? {
            name:
              (
                raw?.eip712Domain?.name ??
                live?.eip712Domain?.name ??
                ''
              ).trim() || undefined,
            version:
              (
                raw?.eip712Domain?.version ??
                live?.eip712Domain?.version ??
                ''
              ).trim() || undefined,
          }
        : undefined,
  };
}

function catalogPrice(
  priceUnit: string | undefined,
  priceRaw: string,
  live?: ServiceOfferPrice
): ServiceOfferPrice {
  const out: ServiceOfferPrice = { ...(live ?? {}) };
  if (!priceRaw) return out;

  switch (priceUnit) {
    case 'perMTok':
    case 'perMtok':
    case 'mtok':
      out.perMTok = priceRaw;
      break;
    case 'perHour':
    case 'hour':
      out.perHour = priceRaw;
      break;
    case 'perEpoch':
    case 'epoch':
      out.perEpoch = priceRaw;
      break;
    case 'agent-turn':
    case 'perRequest':
    case 'request':
    default:
      out.perRequest = priceRaw;
      break;
  }
  return out;
}

function normalizePriceDisplay(value: string | undefined): string {
  const trimmed = (value ?? '').trim();
  if (!trimmed) return '';
  const numericPrefix = /^(\d+(?:\.\d+)?)/.exec(trimmed);
  return numericPrefix?.[1] ?? trimmed;
}

function dedupeStrings(values: readonly string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const trimmed = value.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    out.push(trimmed);
  }
  return out;
}
