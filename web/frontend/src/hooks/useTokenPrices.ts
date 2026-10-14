import { useQuery } from '@tanstack/react-query';
import { COINGECKO_IDS } from '../lib/constants/tokens.ts';

export const TOKEN_PRICE_REFRESH_MS = 5 * 60 * 1000;

export interface TokenPrice {
  price: number;
  change24h: number;
}

export interface TokenPrices {
  ethereum: TokenPrice;
  obol: TokenPrice;
}

type CoinGeckoPriceResponse = Record<
  string,
  {
    usd?: number;
    usd_24h_change?: number;
  }
>;

const EMPTY_TOKEN_PRICES: TokenPrices = {
  ethereum: { price: 0, change24h: 0 },
  obol: { price: 0, change24h: 0 },
};

export interface UseTokenPricesOptions {
  enabled?: boolean;
}

export function isObolSymbol(symbol: string | undefined): boolean {
  return symbol?.trim().toUpperCase() === 'OBOL';
}

export function formatUsdcAmount(amount: number): string {
  if (!Number.isFinite(amount) || amount < 0) return '';
  return amount.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: amount >= 1 ? 2 : 6,
  });
}

export function obolAmountToUsdc(
  amount: string | number,
  obolUsdPrice: number | null | undefined
): string | null {
  if (
    obolUsdPrice === null ||
    obolUsdPrice === undefined ||
    !Number.isFinite(obolUsdPrice) ||
    obolUsdPrice <= 0
  ) {
    return null;
  }

  const parsedAmount =
    typeof amount === 'number'
      ? amount
      : Number(String(amount).replace(/,/g, '').trim());
  if (!Number.isFinite(parsedAmount) || parsedAmount < 0) return null;

  return formatUsdcAmount(parsedAmount * obolUsdPrice);
}

export function normalizeTokenPrices(
  data: CoinGeckoPriceResponse
): TokenPrices {
  return {
    ethereum: {
      price: data[COINGECKO_IDS.ETHEREUM]?.usd ?? 0,
      change24h: data[COINGECKO_IDS.ETHEREUM]?.usd_24h_change ?? 0,
    },
    obol: {
      price: data[COINGECKO_IDS.OBOL]?.usd ?? 0,
      change24h: data[COINGECKO_IDS.OBOL]?.usd_24h_change ?? 0,
    },
  };
}

export async function fetchTokenPrices(): Promise<TokenPrices> {
  const response = await fetch(
    `https://api.coingecko.com/api/v3/simple/price?ids=${COINGECKO_IDS.OBOL},${COINGECKO_IDS.ETHEREUM}&vs_currencies=usd&include_24hr_change=true`
  );

  if (!response.ok) {
    throw new Error('Failed to fetch token prices');
  }

  const data = (await response.json()) as CoinGeckoPriceResponse;
  return normalizeTokenPrices(data);
}

export function useTokenPrices(options: UseTokenPricesOptions = {}) {
  const query = useQuery({
    queryKey: ['token-prices', 'usd'],
    queryFn: fetchTokenPrices,
    staleTime: TOKEN_PRICE_REFRESH_MS,
    refetchInterval: TOKEN_PRICE_REFRESH_MS,
    refetchIntervalInBackground: true,
    enabled: options.enabled ?? true,
  });

  const tokenPrices = query.data ?? EMPTY_TOKEN_PRICES;
  const error = query.error instanceof Error ? query.error.message : null;

  return {
    tokenPrices,
    loading: query.isLoading,
    error,
    // Convenience getters for backward compatibility
    ethPrice: tokenPrices.ethereum.price,
    ethChange24h: tokenPrices.ethereum.change24h,
    obolPrice: tokenPrices.obol.price,
    obolChange24h: tokenPrices.obol.change24h,
  };
}
