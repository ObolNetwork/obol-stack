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
    setError(null);
    setUnreachable(false);

    // Single-path, no-throw control flow so we never produce an unhandled
    // promise rejection or a console.error — both pop Next's dev overlay
    // even when the failure is "expected" (no cluster running).
    let response: Response;
    try {
      response = await fetch('/api/dashboard');
    } catch {
      // Network-level failure: TypeError "failed to fetch", AbortError,
      // DOMException. The stack isn't answering at all.
      setUnreachable(true);
      setError('Obol Stack unreachable');
      setLoading(false);
      return;
    }

    if (!response.ok) {
      if (response.status >= 500) {
        setUnreachable(true);
        setError(`Obol Stack unreachable (HTTP ${response.status})`);
      } else {
        setError(`Failed to fetch dashboard data: ${response.status}`);
      }
      setLoading(false);
      return;
    }

    try {
      const dashboardData = await response.json();
      setData(dashboardData);
    } catch {
      setError('Bad dashboard response (invalid JSON)');
    }
    setLoading(false);
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
