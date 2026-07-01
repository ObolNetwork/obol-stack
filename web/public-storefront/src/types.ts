export interface ServiceAsset {
  address?: string;
  symbol?: string;
  decimals?: number;
  transferMethod?: string;
}

// ServicePayment is one accepted (price, payTo, network, asset) option.
// Mirrors the catalog's ServiceCatalogPaymentOption. A service advertising
// several is paid in any one of them — the buyer picks.
export interface ServicePayment {
  price: string;
  priceRaw?: string;
  priceUnit?: string;
  payTo: string;
  network: string;
  asset?: ServiceAsset;
}

export interface Service {
  name: string;
  namespace: string;
  type: string;
  model?: string;
  endpoint: string;
  price: string;
  priceRaw?: string;
  payTo: string;
  network: string;
  // asset is the resolved settlement token. Mirrors the controller's
  // ServiceCatalogAsset payload — when present, .symbol is the source of
  // truth for "what does this offer charge in" (e.g. "OBOL", "USDC"). The
  // legacy `network === "ethereum" ? "OBOL" : "USDC"` heuristic was wrong
  // for OBOL on base-sepolia and any non-mainnet OBOL deployment.
  asset?: ServiceAsset;
  description: string;
  // skills are the OASF / buy-x402 skill names this offer advertises.
  // For type=agent offers it mirrors AgentResolution.Skills (the
  // resolved allow-list from the linked Agent CR); for non-agent
  // offers it mirrors spec.registration.skills. Rendered as pills on
  // the ServiceCard, matching the 402 page.
  skills?: string[];
  // payments is every accepted payment option (one per currency/network).
  // payments[0] mirrors the flat price/network/payTo/asset fields. Absent on
  // catalogs predating multi-currency — fall back to the flat fields.
  payments?: ServicePayment[];
  // category groups the service into a storefront section (e.g. "demo").
  // Absent/empty means the default section. Mirrors spec.listing.category —
  // demo services are just category="demo", not a special case.
  category?: string;
  // weight orders services within a category; higher sorts earlier.
  weight?: number;
}

export interface StorefrontProfile {
  displayName: string;
  tagline: string;
  logoUrl: string;
}
