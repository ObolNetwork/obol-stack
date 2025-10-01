import { useState, useEffect } from 'react';

export function useEthPrice() {
  const [ethPrice, setEthPrice] = useState<number>(0);
  const [ethChange24h, setEthChange24h] = useState<number>(0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const fetchEthPrice = async () => {
      setLoading(true);
      setError(null);

      try {
        const response = await fetch(
          'https://api.coingecko.com/api/v3/simple/price?ids=ethereum&vs_currencies=usd&include_24hr_change=true'
        );

        if (!response.ok) {
          throw new Error('Failed to fetch ETH price');
        }

        const data = await response.json();
        setEthPrice(data.ethereum?.usd || 3200);
        setEthChange24h(data.ethereum?.usd_24h_change || 0);
      } catch (err) {
        console.error('Failed to fetch ETH price:', err);
        setError(err instanceof Error ? err.message : 'Unknown error');
        setEthPrice(3200); // Fallback price
        setEthChange24h(0); // Fallback change
      } finally {
        setLoading(false);
      }
    };

    fetchEthPrice();
  }, []);

  return { ethPrice, ethChange24h, loading, error };
}
