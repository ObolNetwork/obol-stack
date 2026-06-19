export interface ServiceAsset {
  address?: string;
  symbol?: string;
  decimals?: number;
  transferMethod?: string;
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
  // category groups the service into a storefront section (e.g. "demo").
  // Absent/empty means the default section. Mirrors spec.listing.category —
  // demo services are just category="demo", not a special case.
  category?: string;
  // weight orders services within a category; higher sorts earlier.
  weight?: number;
}
