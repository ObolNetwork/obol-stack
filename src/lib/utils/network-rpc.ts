import {
  ErpcNetwork,
  ErpcUpstream,
  ChainName,
  ClientStatusType,
} from '../../types/dashboard';

export const TESTNETS = new Set(['hoodi', 'sepolia', 'base-sepolia']);
export const NETWORK_ORDER = [
  'mainnet',
  'base',
  'sepolia',
  'hoodi',
  'base-sepolia',
  'aztec',
];

export function sortNetworks(networks: ErpcNetwork[]): ErpcNetwork[] {
  return [...networks].sort((a, b) => {
    const tier = (alias: string) => {
      if (alias === 'mainnet') return 0;
      if (!TESTNETS.has(alias)) return 1; // production (base, aztec, …)
      return 2; // testnet
    };
    const ta = tier(a.alias);
    const tb = tier(b.alias);
    if (ta !== tb) return ta - tb;
    // Within same tier, follow known order
    const ai = NETWORK_ORDER.indexOf(a.alias);
    const bi = NETWORK_ORDER.indexOf(b.alias);
    if (ai === -1 && bi === -1) return a.alias.localeCompare(b.alias);
    if (ai === -1) return 1;
    if (bi === -1) return -1;
    return ai - bi;
  });
}

export function sortChainNames(chains: ChainName[]): ChainName[] {
  return [...chains].sort((a, b) => {
    const ai = NETWORK_ORDER.indexOf(a);
    const bi = NETWORK_ORDER.indexOf(b);
    const aTestnet = TESTNETS.has(a);
    const bTestnet = TESTNETS.has(b);
    if (aTestnet && !bTestnet) return 1;
    if (!bTestnet && aTestnet) return -1;
    if (ai === -1 && bi === -1) return a.localeCompare(b);
    if (ai === -1) return 1;
    if (bi === -1) return -1;
    return ai - bi;
  });
}

export function networkToChainName(alias: string): ChainName | null {
  if (
    alias === 'mainnet' ||
    alias === 'sepolia' ||
    alias === 'hoodi' ||
    alias === 'base' ||
    alias === 'base-sepolia' ||
    alias === 'aztec'
  ) {
    return alias as ChainName;
  }
  return null;
}

export function networkToStatus(state: string): ClientStatusType {
  return state === 'OK' ? 'synced' : 'error';
}

export function chainIdFromEvmId(id: string): number | null {
  const match = id.match(/^evm:(\d+)$/);
  return match ? parseInt(match[1], 10) : null;
}

export function erpcUrl(): string {
  return process.env.NEXT_PUBLIC_ERPC_URL ?? 'https://obol.stack/rpc';
}

/** Group upstreams by their network ID (e.g. "evm:1") */
export function groupUpstreamsByNetwork(
  upstreams: ErpcUpstream[]
): Map<string, ErpcUpstream[]> {
  const groups = new Map<string, ErpcUpstream[]>();
  for (const upstream of upstreams) {
    const existing = groups.get(upstream.network) ?? [];
    groups.set(upstream.network, [...existing, upstream]);
  }
  return groups;
}

export function formatMs(seconds: number): string {
  return `${Math.round(seconds * 1000)}ms`;
}

export function formatRate(rate: number): string {
  if (rate === 0) return '0%';
  return `${(rate * 100).toFixed(2)}%`;
}
