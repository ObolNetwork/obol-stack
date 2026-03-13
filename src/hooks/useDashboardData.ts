'use client';

import { useState, useEffect } from 'react';
import { DashboardData } from '../types/dashboard';

interface UseDashboardDataResult {
  data: DashboardData | null;
  loading: boolean;
  error: string | null;
  refetch: () => void;
}

export function useDashboardData(
  refreshInterval = 10000
): UseDashboardDataResult {
  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchData = async () => {
    try {
      setError(null);
      const response = await fetch('/api/dashboard');

      if (!response.ok) {
        throw new Error(`Failed to fetch dashboard data: ${response.status}`);
      }

      const dashboardData = await response.json();
      setData({
        ...dashboardData,
        // remoteNode: {
        //   networks: [{ id: "evm:560048", alias: "hoodi", state: "OK" }],
        //   networkCount: 1,
        //   upstreamCount: 2,
        //   upstreams: [
        //     {
        //       id: "hoodi-geth",
        //       network: "evm:560048",
        //       metrics: {
        //         cordoned: false,
        //         lastCordonedReason: null,
        //         errorRate: 0,
        //         errorsTotal: 0,
        //         requestsTotal: 1420,
        //         misbehaviorRate: 0,
        //         misbehaviorsTotal: 0,
        //         blockHeadLag: 0,
        //         finalizationLag: 0,
        //         throttledRate: 0,
        //         selfRateLimitedTotal: 0,
        //         remoteRateLimitedTotal: 0,
        //         responseQuantiles: { p50: 0.012, p70: 0.018, p90: 0.035, p95: 0.054, p99: 0.120 }
        //       }
        //     },
        //     {
        //       id: "hoodi-lighthouse",
        //       network: "evm:560048",
        //       metrics: {
        //         cordoned: false,
        //         lastCordonedReason: null,
        //         errorRate: 0.002,
        //         errorsTotal: 3,
        //         requestsTotal: 580,
        //         misbehaviorRate: 0,
        //         misbehaviorsTotal: 0,
        //         blockHeadLag: 1,
        //         finalizationLag: 0,
        //         throttledRate: 0,
        //         selfRateLimitedTotal: 0,
        //         remoteRateLimitedTotal: 0,
        //         responseQuantiles: { p50: 0.025, p70: 0.042, p90: 0.088, p95: 0.140, p99: 0.310 }
        //       }
        //     }
        //   ]
        // },
        // localNodes: [
        //   {
        //     chainName: "hoodi",
        //     app: "ethereum",
        //     appId: "ethereum-hoodi",
        //     nodeMode: "fullMode",
        //     status: "synced",
        //     blocks: 22034210,
        //     finalized: 22034176,
        //     participation: 99.2,
        //     distance: 2,
        //     clients: [
        //       { name: "geth", version: "1.15.7", peers: 50, status: "synced", externalUrl: "http://localhost:8545", internalUrl: "http://geth:8545" },
        //       { name: "lighthouse", version: "5.3.0", peers: 80, status: "synced", externalUrl: "http://localhost:5052", internalUrl: "http://lighthouse:5052" }
        //     ]
        //   },
        //   {
        //     chainName: "hoodi",
        //     app: "helios",
        //     appId: "helios-hoodi",
        //     nodeMode: "lightMode",
        //     status: "synced",
        //     blocks: 22034208,
        //     clients: [
        //       { name: "helios", version: "0.1.0", peers: 0, status: "synced" }
        //     ]
        //   }
        // ]
      });
    } catch (err) {
      const errorMessage =
        err instanceof Error ? err.message : 'Unknown error occurred';
      setError(errorMessage);
      console.error('Dashboard data fetch error:', err);
    } finally {
      setLoading(false);
    }
  };

  const refetch = () => {
    setLoading(true);
    fetchData();
  };

  useEffect(() => {
    // Initial fetch
    fetchData();

    // Set up polling interval
    const interval = setInterval(fetchData, refreshInterval);

    // Cleanup interval on unmount
    return () => clearInterval(interval);
  }, [refreshInterval]);

  return {
    data,
    loading,
    error,
    refetch,
  };
}
