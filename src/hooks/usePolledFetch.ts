'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

export interface UsePolledFetchOptions<T> {
  /** Poll interval. Defaults to 30s to match the marketplace pages. */
  intervalMs?: number;
  /** Extract the typed payload from the parsed JSON body. */
  mapResponse: (body: unknown) => T;
  /** Fallback error message when the thrown value isn't an Error. */
  errorFallback?: string;
  /**
   * Re-enter the loading state at the start of every poll (MyPurchasesPage
   * semantics). When false (default), `loading` is only true until the first
   * response lands (MyListingsPage semantics).
   */
  loadingOnPoll?: boolean;
}

export interface UsePolledFetchResult<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
  refetch: () => Promise<void>;
}

/**
 * Shared 30s fetch-polling loop used by the My Listings / My Purchases pages.
 * Reproduces the inline useEffect both pages used to carry: `no-store` fetch,
 * error message extraction from the JSON `error` field, a cancelled flag so
 * unmounts never setState, and interval cleanup.
 */
export function usePolledFetch<T>(
  url: string,
  {
    intervalMs = 30_000,
    mapResponse,
    errorFallback = 'Request failed',
    loadingOnPoll = false,
  }: UsePolledFetchOptions<T>
): UsePolledFetchResult<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const cancelledRef = useRef(false);
  // Callers pass inline arrows; keep `load` stable so the interval isn't
  // torn down and recreated on every render.
  const mapRef = useRef(mapResponse);
  mapRef.current = mapResponse;

  const load = useCallback(async () => {
    if (loadingOnPoll) setLoading(true);
    setError(null);
    try {
      const r = await fetch(url, { cache: 'no-store' });
      const body = (await r.json()) as { error?: string };
      if (cancelledRef.current) return;
      if (!r.ok) throw new Error(body.error ?? `HTTP ${r.status}`);
      setData(mapRef.current(body));
    } catch (e) {
      if (cancelledRef.current) return;
      setError(e instanceof Error ? e.message : errorFallback);
    } finally {
      if (!cancelledRef.current) setLoading(false);
    }
  }, [url, errorFallback, loadingOnPoll]);

  useEffect(() => {
    cancelledRef.current = false;
    load();
    const id = setInterval(load, intervalMs);
    return () => {
      cancelledRef.current = true;
      clearInterval(id);
    };
  }, [load, intervalMs]);

  return { data, loading, error, refetch: load };
}
