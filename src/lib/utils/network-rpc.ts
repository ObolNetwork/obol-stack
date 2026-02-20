import {
  ErpcNetwork,
  ChainName,
  ClientStatusType,
} from '../../types/dashboard';

export const TESTNETS = new Set(['hoodi', 'sepolia']);
export const NETWORK_ORDER = ['mainnet', 'sepolia', 'hoodi'];

export function sortNetworks(networks: ErpcNetwork[]): ErpcNetwork[] {
  return [...networks].sort((a, b) => {
    const ai = NETWORK_ORDER.indexOf(a.alias);
    const bi = NETWORK_ORDER.indexOf(b.alias);
    if (ai === -1 && bi === -1) return a.alias.localeCompare(b.alias);
    if (ai === -1) return 1;
    if (bi === -1) return -1;
    return ai - bi;
  });
}

export function networkToChainName(alias: string): ChainName | null {
  if (alias === 'mainnet' || alias === 'sepolia' || alias === 'hoodi') {
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
