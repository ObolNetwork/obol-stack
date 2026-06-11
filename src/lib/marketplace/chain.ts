import type { ChainName } from '@/types/dashboard';

const VALID: ReadonlyArray<ChainName> = [
  'mainnet',
  'sepolia',
  'hoodi',
  'base',
  'base-sepolia',
  'aztec',
];

const ALIASES: Record<string, ChainName> = {
  ethereum: 'mainnet',
  eth: 'mainnet',
  'base mainnet': 'base',
  'eip155:1': 'mainnet',
  'eip155:11155111': 'sepolia',
  'eip155:8453': 'base',
  'eip155:84532': 'base-sepolia',
};

/**
 * Map a freeform payment-network string (`spec.payment.network` from
 * ServiceOffer/PurchaseRequest CRs, or a CAIP-2 form) to a ChainName the
 * shared ChainIcon atom understands. Returns undefined for unknown chains
 * so callers can skip the icon and just render the raw string.
 */
export function toChainName(
  value: string | null | undefined
): ChainName | undefined {
  if (!value) return undefined;
  const lower = value.toLowerCase();
  if (VALID.includes(lower as ChainName)) return lower as ChainName;
  return ALIASES[lower];
}

/** Legacy, CAIP-2, and CRD aliases for the same chain (Prom join keys). */
const SETTLEMENT_CHAIN_ALIASES: Record<ChainName, readonly string[]> = {
  mainnet: ['mainnet', 'ethereum', 'eth', 'eip155:1'],
  sepolia: ['sepolia', 'eip155:11155111'],
  hoodi: ['hoodi'],
  base: ['base', 'base-mainnet', 'eip155:8453'],
  'base-sepolia': ['base-sepolia', 'eip155:84532'],
  aztec: ['aztec'],
};

/** Network strings to try when joining PurchaseRequests to Prometheus gauges. */
export function chainLookupAliases(network: string): string[] {
  const lower = network.trim().toLowerCase();
  const canonical = toChainName(lower);
  if (canonical) return [...SETTLEMENT_CHAIN_ALIASES[canonical]];
  return [lower];
}
