import { mainnet, base, baseSepolia } from 'wagmi/chains';
import { hoodi } from './wagmi';

export type BalanceTokenSymbol = 'ETH' | 'WETH' | 'OBOL' | 'USDC';

export interface BalanceToken {
  symbol: BalanceTokenSymbol;
  address?: `0x${string}`;
  decimals: number;
}

export interface ChainBalanceConfig {
  chainId: number;
  chainName: string;
  shortName: string;
  explorerUrl: string;
  tokens: BalanceToken[];
}

export const AGENT_BALANCE_CHAINS: ChainBalanceConfig[] = [
  {
    chainId: mainnet.id,
    chainName: 'Ethereum',
    shortName: 'mainnet',
    explorerUrl: 'https://etherscan.io',
    tokens: [
      { symbol: 'ETH', decimals: 18 },
      {
        symbol: 'WETH',
        address: '0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2',
        decimals: 18,
      },
      {
        symbol: 'OBOL',
        address: '0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7',
        decimals: 18,
      },
      {
        symbol: 'USDC',
        address: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
        decimals: 6,
      },
    ],
  },
  {
    chainId: base.id,
    chainName: 'Base',
    shortName: 'base',
    explorerUrl: 'https://basescan.org',
    tokens: [
      { symbol: 'ETH', decimals: 18 },
      {
        symbol: 'WETH',
        address: '0x4200000000000000000000000000000000000006',
        decimals: 18,
      },
      {
        symbol: 'USDC',
        address: '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913',
        decimals: 6,
      },
    ],
  },
  {
    chainId: baseSepolia.id,
    chainName: 'Base Sepolia',
    shortName: 'base-sepolia',
    explorerUrl: 'https://sepolia.basescan.org',
    tokens: [
      { symbol: 'ETH', decimals: 18 },
      {
        symbol: 'WETH',
        address: '0x4200000000000000000000000000000000000006',
        decimals: 18,
      },
      {
        symbol: 'USDC',
        address: '0x036cbd53842c5426634e7929541ec2318f3dcf7e',
        decimals: 6,
      },
    ],
  },
  {
    chainId: hoodi.id,
    chainName: 'Hoodi',
    shortName: 'hoodi',
    explorerUrl: 'https://hoodi.etherscan.io',
    tokens: [{ symbol: 'ETH', decimals: 18 }],
  },
];

export function explorerUrlFor(
  chain: ChainBalanceConfig,
  token: BalanceToken,
  walletAddress: string
): string {
  if (!token.address) {
    return `${chain.explorerUrl}/address/${walletAddress}`;
  }
  return `${chain.explorerUrl}/token/${token.address}?a=${walletAddress}`;
}
