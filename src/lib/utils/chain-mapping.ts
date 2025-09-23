/**
 * Chain ID to chain information mapping utilities
 */

import { CHAINS, NETWORKS, ChainType, NetworkType } from '@/types/dashboard';

export interface ChainInfo {
  chainName: ChainType;
  network: NetworkType;
}

/**
 * Known chain IDs and their corresponding information
 */
const CHAIN_ID_MAP: Record<number, ChainInfo> = {
  1: {
    chainName: CHAINS.ETHEREUM,
    network: NETWORKS.MAINNET,
  },
  11155111: {
    chainName: CHAINS.SEPOLIA,
    network: NETWORKS.TESTNET,
  },
  560048: {
    chainName: CHAINS.HOODI,
    network: NETWORKS.TESTNET,
  },
  // Add more chains as needed
};

/**
 * Get chain information from chain ID
 * @param chainId - The chain ID from RPC call
 * @returns Chain information including name and network type
 */
export function getChainInfoFromId(chainId: number): ChainInfo {
  const knownChain = CHAIN_ID_MAP[chainId];

  if (knownChain) {
    return knownChain;
  }

  // For unknown chains, return a generic mapping
  return {
    chainName: `chain-${chainId}` as ChainType,
    network: NETWORKS.TESTNET, // Default unknown chains to testnet
  };
}

/**
 * Check if a chain ID is a known mainnet
 * @param chainId - The chain ID to check
 * @returns true if it's a known mainnet chain
 */
export function isMainnetChain(chainId: number): boolean {
  const chainInfo = getChainInfoFromId(chainId);
  return chainInfo.network === NETWORKS.MAINNET;
}

/**
 * Check if a chain ID is a known testnet
 * @param chainId - The chain ID to check
 * @returns true if it's a known testnet chain
 */
export function isTestnetChain(chainId: number): boolean {
  const chainInfo = getChainInfoFromId(chainId);
  return chainInfo.network === NETWORKS.TESTNET;
}
