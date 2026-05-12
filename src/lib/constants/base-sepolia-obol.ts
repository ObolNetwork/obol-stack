import { baseSepolia } from 'wagmi/chains';

export type BaseSepoliaObolConfig = {
  chainId: number;
  chainName: string;
  symbol: 'OBOL';
  decimals: 18;
  claimAmountLabel: string;
  tokenAddress?: `0x${string}`;
  faucetAddress?: `0x${string}`;
  explorerUrl: string;
};

export const BASE_SEPOLIA_OBOL_DEFAULT: BaseSepoliaObolConfig = {
  chainId: baseSepolia.id,
  chainName: 'Base Sepolia',
  symbol: 'OBOL',
  decimals: 18,
  claimAmountLabel: '5 OBOL',
  explorerUrl: 'https://sepolia.basescan.org',
};

export const BASE_SEPOLIA_OBOL_FAUCET_ABI = [
  {
    type: 'function',
    name: 'claim',
    stateMutability: 'nonpayable',
    inputs: [{ name: 'to', type: 'address' }],
    outputs: [],
  },
] as const;
