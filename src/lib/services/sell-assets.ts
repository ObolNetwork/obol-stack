import { TOKEN_ADDRESSES } from '../constants/tokens.ts';
import type { ServiceOfferAsset } from '@/types/sell';

export type SellPaymentToken = 'USDC' | 'OBOL';

type Env = Record<string, string | undefined>;

interface AssetDefinition extends ServiceOfferAsset {
  symbol: SellPaymentToken;
  address: string;
  decimals: number;
  transferMethod: string;
  eip712Name: string;
  eip712Version: string;
}

export interface ResolvedSellAsset {
  asset?: AssetDefinition;
  error?: string;
}

const USDC_BY_CHAIN: Record<string, AssetDefinition> = {
  base: {
    address: '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913',
    symbol: 'USDC',
    decimals: 6,
    transferMethod: 'eip3009',
    eip712Name: 'USD Coin',
    eip712Version: '2',
  },
  'base-sepolia': {
    address: '0x036CbD53842c5426634e7929541eC2318f3dCF7e',
    symbol: 'USDC',
    decimals: 6,
    transferMethod: 'eip3009',
    eip712Name: 'USDC',
    eip712Version: '2',
  },
  mainnet: {
    address: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
    symbol: 'USDC',
    decimals: 6,
    transferMethod: 'eip3009',
    eip712Name: 'USD Coin',
    eip712Version: '2',
  },
  ethereum: {
    address: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
    symbol: 'USDC',
    decimals: 6,
    transferMethod: 'eip3009',
    eip712Name: 'USD Coin',
    eip712Version: '2',
  },
};

const OBOL_ENV_BY_CHAIN: Record<string, string[]> = {
  mainnet: [
    'OBOL_TOKEN_MAINNET',
    'OBOL_TOKEN_ETHEREUM',
    'NEXT_PUBLIC_OBOL_TOKEN_MAINNET',
    'NEXT_PUBLIC_OBOL_TOKEN_ETHEREUM',
  ],
  ethereum: [
    'OBOL_TOKEN_ETHEREUM',
    'OBOL_TOKEN_MAINNET',
    'NEXT_PUBLIC_OBOL_TOKEN_ETHEREUM',
    'NEXT_PUBLIC_OBOL_TOKEN_MAINNET',
  ],
  base: ['OBOL_TOKEN_BASE', 'NEXT_PUBLIC_OBOL_TOKEN_BASE'],
  'base-sepolia': [
    'OBOL_TOKEN_BASE_SEPOLIA',
    'BASE_SEPOLIA_OBOL_TOKEN_ADDRESS',
    'NEXT_PUBLIC_OBOL_TOKEN_BASE_SEPOLIA',
    'NEXT_PUBLIC_BASE_SEPOLIA_OBOL_TOKEN_ADDRESS',
  ],
};

export function normalizeSellToken(value: unknown): SellPaymentToken {
  return value === 'OBOL' ? 'OBOL' : 'USDC';
}

export function resolveSellAsset(
  chain: string,
  token: SellPaymentToken,
  env: Env = process.env
): ResolvedSellAsset {
  const normalizedChain = normalizeChain(chain);
  if (token === 'USDC') {
    const asset = USDC_BY_CHAIN[normalizedChain];
    if (!asset) {
      return { error: `USDC is not configured for chain "${chain}".` };
    }
    return { asset };
  }

  const address = obolAddressFor(normalizedChain, env);
  if (!address) {
    return {
      error:
        `OBOL is not configured for chain "${chain}". ` +
        'Provide a per-chain OBOL token address before creating this offer.',
    };
  }
  return {
    asset: {
      address,
      symbol: 'OBOL',
      decimals: 18,
      transferMethod: 'permit2',
      eip712Name: 'Obol Network',
      eip712Version: '1',
    },
  };
}

function normalizeChain(chain: string): string {
  switch (chain) {
    case 'ethereum':
      return 'mainnet';
    default:
      return chain;
  }
}

function obolAddressFor(chain: string, env: Env): string | undefined {
  const envKeys = OBOL_ENV_BY_CHAIN[chain] ?? [];
  for (const key of envKeys) {
    const value = env[key]?.trim();
    if (isAddress(value)) return value;
  }
  if (chain === 'mainnet') return TOKEN_ADDRESSES.OBOL;
  return undefined;
}

function isAddress(value: string | undefined): value is string {
  return Boolean(value && /^0x[0-9a-fA-F]{40}$/.test(value));
}
