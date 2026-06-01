import { TOKEN_ADDRESSES } from '../constants/tokens.ts';
import type { ServiceOfferAsset } from '@/types/sell';

export type SellPaymentToken = 'USDC' | 'OBOL';
export type SellPaymentChain = 'base' | 'base-sepolia' | 'mainnet';

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

export interface SellPaymentSelection {
  chain: SellPaymentChain;
  token: SellPaymentToken;
  error?: string;
  autoSwitched?: boolean;
}

export const BASE_SEPOLIA_OBOL_ADDRESS =
  '0x0a09371a8b011d5110656ceBCc70603e53FD2c78';

export const CLIENT_SELL_ASSET_ENV: Env = {
  NEXT_PUBLIC_OBOL_TOKEN_MAINNET: process.env.NEXT_PUBLIC_OBOL_TOKEN_MAINNET,
  NEXT_PUBLIC_OBOL_TOKEN_ETHEREUM: process.env.NEXT_PUBLIC_OBOL_TOKEN_ETHEREUM,
  NEXT_PUBLIC_OBOL_TOKEN_BASE: process.env.NEXT_PUBLIC_OBOL_TOKEN_BASE,
  NEXT_PUBLIC_OBOL_TOKEN_BASE_SEPOLIA:
    process.env.NEXT_PUBLIC_OBOL_TOKEN_BASE_SEPOLIA,
  NEXT_PUBLIC_BASE_SEPOLIA_OBOL_TOKEN_ADDRESS:
    process.env.NEXT_PUBLIC_BASE_SEPOLIA_OBOL_TOKEN_ADDRESS,
};

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

const DEFAULT_OBOL_BY_CHAIN: Partial<Record<SellPaymentChain, string>> = {
  mainnet: TOKEN_ADDRESSES.OBOL,
  'base-sepolia': BASE_SEPOLIA_OBOL_ADDRESS,
};

const SELL_PAYMENT_CHAINS: SellPaymentChain[] = [
  'base',
  'base-sepolia',
  'mainnet',
];

export function normalizeSellToken(value: unknown): SellPaymentToken {
  return value === 'OBOL' ? 'OBOL' : 'USDC';
}

export function normalizeSellChain(chain: string): string {
  switch (chain) {
    case 'ethereum':
    case 'ethereum-mainnet':
      return 'mainnet';
    case 'base-mainnet':
      return 'base';
    default:
      return chain;
  }
}

export function isSellTokenSupportedOnChain(
  chain: string,
  token: SellPaymentToken,
  env: Env = process.env
): boolean {
  return !resolveSellAsset(chain, token, env).error;
}

export function reconcileSellPaymentSelection(
  chain: string,
  token: SellPaymentToken,
  env: Env = process.env
): SellPaymentSelection {
  const normalizedChain = normalizeSellChain(chain);
  if (
    isSellPaymentChain(normalizedChain) &&
    isSellTokenSupportedOnChain(normalizedChain, token, env)
  ) {
    return { chain: normalizedChain, token };
  }

  if (token === 'OBOL' && isSellTokenSupportedOnChain('mainnet', token, env)) {
    return { chain: 'mainnet', token, autoSwitched: true };
  }

  const { error } = resolveSellAsset(normalizedChain, token, env);
  return {
    chain: isSellPaymentChain(normalizedChain) ? normalizedChain : 'base',
    token,
    error,
  };
}

export function resolveSellAsset(
  chain: string,
  token: SellPaymentToken,
  env: Env = process.env
): ResolvedSellAsset {
  const normalizedChain = normalizeSellChain(chain);
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

function obolAddressFor(chain: string, env: Env): string | undefined {
  const envKeys = OBOL_ENV_BY_CHAIN[chain] ?? [];
  for (const key of envKeys) {
    const value = env[key]?.trim();
    if (isAddress(value)) return value;
  }
  if (isSellPaymentChain(chain)) return DEFAULT_OBOL_BY_CHAIN[chain];
  return undefined;
}

function isAddress(value: string | undefined): value is string {
  return Boolean(value && /^0x[0-9a-fA-F]{40}$/.test(value));
}

function isSellPaymentChain(chain: string): chain is SellPaymentChain {
  return SELL_PAYMENT_CHAINS.includes(chain as SellPaymentChain);
}
