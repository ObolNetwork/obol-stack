'use client';

import { useState, useEffect } from 'react';
import { DashboardData } from '../types/dashboard';

interface UseDashboardDataResult {
  data: DashboardData | null;
  loading: boolean;
  error: string | null;
  /**
   * True when the last fetch failed because the cluster wasn't reachable
   * (network error or HTTP 5xx). Lets the dashboard render the same
   * StackUnreachableState the marketplace uses, instead of bubbling
   * "Failed to fetch dashboard data: 503" out to the console.
   */
  unreachable: boolean;
  refetch: () => void;
}

export function useDashboardData(
  refreshInterval = 10000
): UseDashboardDataResult {
  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [unreachable, setUnreachable] = useState(false);

  const fetchData = async () => {
    try {
      setError(null);
      setUnreachable(false);
      const response = await fetch('/api/dashboard');

      if (!response.ok) {
        if (response.status >= 500) {
          setUnreachable(true);
          throw new Error(`Obol Stack unreachable (HTTP ${response.status})`);
        }
        throw new Error(`Failed to fetch dashboard data: ${response.status}`);
      }

      const dashboardData = await response.json();
      setData(dashboardData);
    } catch (err) {
      // Network-level failures (TypeError "failed to fetch", AbortError)
      // also mean the stack isn't answering — surface them uniformly.
      const isNetworkFailure =
        err instanceof TypeError ||
        (err instanceof Error && err.name === 'AbortError');
      if (isNetworkFailure) setUnreachable(true);
      const errorMessage = isNetworkFailure
        ? 'Obol Stack unreachable'
        : err instanceof Error
          ? err.message
          : 'Unknown error occurred';
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
    unreachable,
    refetch,
  };
}
