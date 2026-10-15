import type { AssetBalance } from '../../hooks/useWalletAssetBalances';

const SYMBOL_ORDER: Record<string, number> = {
  ETH: 0,
  WETH: 1,
  OBOL: 2,
  USDC: 3,
};

/** Stable display order: chain id, then token priority (OBOL before USDC). */
export function sortBalances(balances: AssetBalance[]): AssetBalance[] {
  return [...balances].sort((a, b) => {
    if (a.chain.chainId !== b.chain.chainId) {
      return a.chain.chainId - b.chain.chainId;
    }
    return (
      (SYMBOL_ORDER[a.token.symbol] ?? 99) -
      (SYMBOL_ORDER[b.token.symbol] ?? 99)
    );
  });
}

/** Group balances by chain for compact multi-token rows. */
export function groupBalancesByChain(
  balances: AssetBalance[]
): AssetBalance[][] {
  const sorted = sortBalances(balances);
  const groups: AssetBalance[][] = [];
  let current: AssetBalance[] = [];
  let chainId: number | null = null;

  for (const balance of sorted) {
    if (chainId !== null && balance.chain.chainId !== chainId) {
      groups.push(current);
      current = [];
    }
    chainId = balance.chain.chainId;
    current.push(balance);
  }
  if (current.length > 0) groups.push(current);
  return groups;
}

export function primaryBalanceForDisplay(
  balances: AssetBalance[]
): AssetBalance | null {
  const sorted = sortBalances(balances);
  return (
    sorted.find(b => b.token.symbol === 'OBOL') ??
    sorted.find(b => b.token.symbol === 'USDC') ??
    sorted[0] ??
    null
  );
}
