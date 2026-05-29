/**
 * Public types for the marketplace foundation. These are the shapes consumed
 * by the Next.js Route Handlers and (in the next PR) by the List and Cards
 * views.
 *
 * The source of truth for the underlying data is the controller-published
 * /api/services.json (see internal/serviceoffercontroller/render.go and
 * internal/schemas/service_catalog.go in obol-stack). This module narrows
 * that storefront-friendly shape into a UI-friendly one.
 */

import type { ServiceOfferType } from '@/types/sell';

export type MarketplacePriceUnit =
  | 'request'
  | 'agent-turn'
  | 'mtok'
  | 'hour'
  | 'epoch';

export type MarketplaceStatus = 'live' | 'pending-registration' | 'degraded';

/**
 * Which catalog backs the marketplace listing.
 *
 * - `obol`: the in-cluster Obol Stack ServiceOffer catalog at
 *   /api/services.json (controller-published).
 * - `erc8004`: the on-chain ERC-8004 Identity Registry, queried directly
 *   from the chain via eRPC. Surfaces every registered agent across the
 *   configured networks, regardless of whether the local cluster hosts it.
 */
export type MarketplaceRegistry = 'obol' | 'erc8004';

export type MarketplaceView = 'list' | 'cards';

export type MarketplaceSort = 'price_asc' | 'price_desc' | 'name' | 'recent';

export interface MarketplaceAsset {
  symbol: string;
  address: string;
  decimals: number;
  transferMethod?: string;
  eip712Domain?: {
    name?: string;
    version?: string;
  };
}

export interface MarketplaceNetwork {
  /** CAIP-2 form, e.g. "eip155:1". Authoritative identifier. */
  caip2: string;
  chainId: number;
  /** Human-friendly alias, e.g. "ethereum", "base-sepolia". */
  alias: string;
}

/**
 * One row in the marketplace listing — the unit both List and Cards render.
 *
 * Names and shapes deliberately track the upstream ServiceCatalogEntry so the
 * mapping in catalog.ts stays trivial. The few derived fields (status,
 * modelOrRuntime, bannerSeed, slug) are computed once on the server.
 */
export interface ServiceListItem {
  /** "<namespace>/<name>" — stable URL slug. */
  slug: string;
  name: string;
  namespace: string;
  type: ServiceOfferType;
  /** Display string: model name for inference, "<runtime> · <model>" for agents. */
  modelOrRuntime: string;
  skills: string[];
  /** Human-readable price ("10", "0.001"). */
  price: string;
  priceUnit: MarketplacePriceUnit;
  /** Atomic-units string, e.g. "10000000000000000000". Useful for buy preflight. */
  priceAtomicUnits: string;
  asset: MarketplaceAsset;
  network: MarketplaceNetwork;
  endpoint: string;
  /** Seller wallet (`spec.payment.payTo`). Drives the identicon + address row. */
  payTo: string;
  status: MarketplaceStatus;
  isDemo: boolean;
  /** RFC3339 drain deadline from /api/services.json. Present only while draining. */
  drainEndsAt?: string;
  description: string;
  /** Deterministic seed (hash of slug) — historically gradient banners,
   *  now also the identicon fallback when payTo is missing. */
  bannerSeed: string;
}

export type MarketplaceSource = 'local' | 'remote';

export interface MarketplaceFilters {
  /**
   * Catalog backend. Defaults to `obol` if omitted. Switching to `erc8004`
   * causes the server to enumerate agents directly from the Identity
   * Registry contracts on mainnet / base / base-sepolia.
   */
  registry?: MarketplaceRegistry;
  /**
   * Filter by origin within the current registry. `local` is anything
   * published by this cluster (namespace not prefixed with `erc8004:`),
   * `remote` is anything indexed from another stack.
   */
  source?: MarketplaceSource[];
  type?: ServiceOfferType[];
  /** CAIP-2 values, e.g. ["eip155:1", "eip155:84532"]. */
  network?: string[];
  /** Asset symbols, e.g. ["USDC", "OBOL"]. */
  asset?: string[];
  /** Inclusive min price (decimal string). */
  minPrice?: string;
  maxPrice?: string;
  /** Free-text search across name, description, model, skills. */
  q?: string;
  sort?: MarketplaceSort;
  page?: number;
  pageSize?: number;
}

export interface FacetBucket {
  value: string;
  count: number;
}

export interface MarketplaceFacets {
  type: FacetBucket[];
  network: FacetBucket[];
  asset: FacetBucket[];
  source: FacetBucket[];
}

export interface ServiceListResponse {
  items: ServiceListItem[];
  total: number;
  facets: MarketplaceFacets;
  /** ISO-8601 timestamp of the underlying catalog snapshot. */
  cachedAt: string;
}

export interface ServiceDetail extends ServiceListItem {
  conditions: Array<{ type: string; status: string; message: string }>;
  wellKnownUrl: string;
  /**
   * Sample 402 envelope shape returned by the gateway on unpaid requests.
   * Useful for the detail page "How to buy" snippet.
   */
  paymentSample: {
    scheme: string;
    network: string;
    payTo: string;
    asset: MarketplaceAsset;
    maxAmountRequired: string;
  };
}
