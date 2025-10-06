import { useState, useEffect } from 'react';
import { COINGECKO_IDS } from '../lib/constants/tokens';

export interface TokenPrice {
  price: number;
  change24h: number;
}

export interface TokenPrices {
  ethereum: TokenPrice;
  obol: TokenPrice;
}

export function useTokenPrices() {
  const [tokenPrices, setTokenPrices] = useState<TokenPrices>({
    ethereum: { price: 0, change24h: 0 },
    obol: { price: 0, change24h: 0 },
  });
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const fetchTokenPrices = async () => {
      setLoading(true);
      setError(null);

      try {
        const response = await fetch(
          `https://api.coingecko.com/api/v3/simple/price?ids=${COINGECKO_IDS.OBOL},${COINGECKO_IDS.ETHEREUM}&vs_currencies=usd&include_24hr_change=true`
        );

        if (!response.ok) {
          throw new Error('Failed to fetch token prices');
        }

        const data = await response.json();

        setTokenPrices({
          ethereum: {
            price: data[COINGECKO_IDS.ETHEREUM]?.usd || 3200, // Fallback price
            change24h: data[COINGECKO_IDS.ETHEREUM]?.usd_24h_change || 0,
          },
          obol: {
            price: data[COINGECKO_IDS.OBOL]?.usd || 0.1, // Fallback price if not listed
            change24h: data[COINGECKO_IDS.OBOL]?.usd_24h_change || 0,
          },
        });
      } catch (err) {
        console.error('Failed to fetch token prices:', err);
        setError(err instanceof Error ? err.message : 'Unknown error');

        // Set fallback values on error
        setTokenPrices({
          ethereum: { price: 3200, change24h: 0 },
          obol: { price: 0.1, change24h: 0 },
        });
      } finally {
        setLoading(false);
      }
    };

    fetchTokenPrices();
  }, []);

  return {
    tokenPrices,
    loading,
    error,
    // Convenience getters for backward compatibility
    ethPrice: tokenPrices.ethereum.price,
    ethChange24h: tokenPrices.ethereum.change24h,
    obolPrice: tokenPrices.obol.price,
    obolChange24h: tokenPrices.obol.change24h,
  };
}
