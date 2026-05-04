'use client';

import { useEffect, useRef, useState } from 'react';
import {
  createPublicClient,
  http,
  erc20Abi,
  formatUnits,
  type Address,
} from 'viem';
import { mainnet, base, baseSepolia } from 'wagmi/chains';
import { hoodi } from '../lib/config/wagmi';
import {
  AGENT_BALANCE_CHAINS,
  type BalanceToken,
  type ChainBalanceConfig,
} from '../lib/config/agent-balance-tokens';

const ERPC_URL = process.env.NEXT_PUBLIC_ERPC_URL ?? '/rpc';
const POLL_MS = 60_000;
const TIMEOUT_MS = 8000;

const CHAIN_BY_ID = {
  [mainnet.id]: mainnet,
  [base.id]: base,
  [baseSepolia.id]: baseSepolia,
  [hoodi.id]: hoodi,
} as const;

const TRANSPORT_URL: Record<number, string> = {
  [mainnet.id]: `${ERPC_URL}/mainnet`,
  [base.id]: `${ERPC_URL}/base`,
  [baseSepolia.id]: `${ERPC_URL}/base-sepolia`,
  [hoodi.id]: `${ERPC_URL}/hoodi`,
};

const MULTICALL3_ADDRESS =
  '0xcA11bde05977b3631167028862bE2a173976CA11' as const;
const MULTICALL3_ABI = [
  {
    inputs: [{ name: 'addr', type: 'address' }],
    name: 'getEthBalance',
    outputs: [{ name: 'balance', type: 'uint256' }],
    stateMutability: 'view',
    type: 'function',
  },
] as const;

export interface AssetBalance {
  chain: ChainBalanceConfig;
  token: BalanceToken;
  raw: bigint;
  formatted: string;
}

function formatBalance(raw: bigint, decimals: number): string {
  const value = Number(formatUnits(raw, decimals));
  if (value === 0) return '0';
  if (value < 0.0001) return '<0.0001';
  if (value < 1) {
    return value.toFixed(4).replace(/\.?0+$/, '');
  }
  if (value < 1000) {
    return value.toFixed(3).replace(/\.?0+$/, '');
  }
  return value.toLocaleString(undefined, { maximumFractionDigits: 2 });
}

async function fetchChainBalances(
  config: ChainBalanceConfig,
  wallet: Address,
  signal: AbortSignal
): Promise<AssetBalance[]> {
  const chain = CHAIN_BY_ID[config.chainId as keyof typeof CHAIN_BY_ID];
  if (!chain) return [];

  const client = createPublicClient({
    chain,
    transport: http(TRANSPORT_URL[config.chainId], {
      timeout: TIMEOUT_MS,
      fetchOptions: { signal },
    }),
  });

  const nativeToken = config.tokens.find(t => !t.address);
  const erc20Tokens = config.tokens.filter(
    (t): t is BalanceToken & { address: `0x${string}` } => Boolean(t.address)
  );

  try {
    if (chain.contracts?.multicall3 && erc20Tokens.length > 0) {
      const contracts = [
        ...(nativeToken
          ? [
              {
                address: MULTICALL3_ADDRESS,
                abi: MULTICALL3_ABI,
                functionName: 'getEthBalance' as const,
                args: [wallet] as const,
              },
            ]
          : []),
        ...erc20Tokens.map(t => ({
          address: t.address,
          abi: erc20Abi,
          functionName: 'balanceOf' as const,
          args: [wallet] as const,
        })),
      ];

      const results = await client.multicall({
        contracts,
        allowFailure: true,
      });

      const out: AssetBalance[] = [];
      let i = 0;
      if (nativeToken) {
        const r = results[i++];
        if (r.status === 'success') {
          const raw = r.result as bigint;
          if (raw > 0n) {
            out.push({
              chain: config,
              token: nativeToken,
              raw,
              formatted: formatBalance(raw, nativeToken.decimals),
            });
          }
        }
      }
      erc20Tokens.forEach((t, j) => {
        const r = results[i + j];
        if (r.status === 'success') {
          const raw = r.result as bigint;
          if (raw > 0n) {
            out.push({
              chain: config,
              token: t,
              raw,
              formatted: formatBalance(raw, t.decimals),
            });
          }
        }
      });
      return out;
    }

    if (!nativeToken) return [];
    const raw = await client.getBalance({ address: wallet });
    if (raw === 0n) return [];
    return [
      {
        chain: config,
        token: nativeToken,
        raw,
        formatted: formatBalance(raw, nativeToken.decimals),
      },
    ];
  } catch {
    return [];
  }
}

function balancesEqual(a: AssetBalance[], b: AssetBalance[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i].chain.chainId !== b[i].chain.chainId) return false;
    if (a[i].token.symbol !== b[i].token.symbol) return false;
    if (a[i].raw !== b[i].raw) return false;
  }
  return true;
}

export function useWalletAssetBalances(address: string | null): {
  balances: AssetBalance[];
  initialLoading: boolean;
} {
  const [balances, setBalances] = useState<AssetBalance[]>([]);
  const [initialLoading, setInitialLoading] = useState(false);
  const balancesRef = useRef<AssetBalance[]>([]);

  useEffect(() => {
    if (!address || !/^0x[a-fA-F0-9]{40}$/.test(address)) {
      setBalances([]);
      balancesRef.current = [];
      setInitialLoading(false);
      return;
    }

    let cancelled = false;
    const wallet = address as Address;
    const controller = new AbortController();

    async function poll(isInitial: boolean) {
      if (isInitial) setInitialLoading(true);
      try {
        const results = await Promise.all(
          AGENT_BALANCE_CHAINS.map(c =>
            fetchChainBalances(c, wallet, controller.signal)
          )
        );
        if (cancelled) return;
        const flat = results.flat();
        if (!balancesEqual(balancesRef.current, flat)) {
          balancesRef.current = flat;
          setBalances(flat);
        }
      } finally {
        if (isInitial && !cancelled) setInitialLoading(false);
      }
    }

    poll(true);
    const interval = setInterval(() => poll(false), POLL_MS);

    return () => {
      cancelled = true;
      controller.abort();
      clearInterval(interval);
    };
  }, [address]);

  return { balances, initialLoading };
}
