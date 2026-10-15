import { clientErpcBaseUrl } from '../utils/cluster-url';
import { AGENT_BALANCE_CHAINS } from './agent-balance-tokens';

/** eRPC path suffix per chain — must match obol-stack eRPC network aliases. */
export const CHAIN_RPC_SUFFIX: Record<number, string> = {
  1: '/mainnet',
  8453: '/base',
  84532: '/base-sepolia',
  560048: '/hoodi',
};

/** Chain ids in AGENT_BALANCE_CHAINS that must have an eRPC suffix configured. */
export const BALANCE_CHAIN_IDS = AGENT_BALANCE_CHAINS.map(c => c.chainId);

export function erpcTransportUrlForChain(chainId: number): string | null {
  const suffix = CHAIN_RPC_SUFFIX[chainId];
  if (suffix === undefined) {
    if (process.env.NODE_ENV !== 'production') {
      console.warn(
        `[chain-rpc] No eRPC path for chainId ${chainId}; add it to CHAIN_RPC_SUFFIX`
      );
    }
    return null;
  }
  return `${clientErpcBaseUrl()}${suffix}`;
}
