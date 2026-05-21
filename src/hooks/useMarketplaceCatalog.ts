'use client';

import { useQuery } from '@tanstack/react-query';

import type {
  MarketplaceFilters,
  ServiceListResponse,
} from '@/lib/marketplace/types';
import {
  STACK_UNREACHABLE,
  StackUnreachableError,
} from '@/lib/marketplace/errors';
import { filtersToSearchParams } from '@/lib/marketplace/query-params';

export { filtersToSearchParams };

const REFRESH_MS = 30_000;

export function useMarketplaceCatalog(filters: MarketplaceFilters) {
  const qs = filtersToSearchParams(filters).toString();
  return useQuery<ServiceListResponse, Error>({
    queryKey: ['marketplace', 'services', qs],
    queryFn: async () => {
      let res: Response;
      try {
        res = await fetch(`/api/marketplace/services${qs ? `?${qs}` : ''}`, {
          headers: { accept: 'application/json' },
        });
      } catch (cause) {
        throw new StackUnreachableError('Obol Stack unreachable', 0, {
          cause,
        });
      }
      if (!res.ok) {
        let body: { error?: string; code?: string } = {};
        try {
          body = (await res.json()) as { error?: string; code?: string };
        } catch {
          // ignore body-parse errors; fall through to status-based check
        }
        if (body.code === STACK_UNREACHABLE || res.status >= 500) {
          throw new StackUnreachableError(
            body.error ?? `Obol Stack unreachable (HTTP ${res.status})`,
            res.status
          );
        }
        throw new Error(body.error ?? `HTTP ${res.status}`);
      }
      return (await res.json()) as ServiceListResponse;
    },
    staleTime: REFRESH_MS,
    refetchInterval: REFRESH_MS,
    refetchOnWindowFocus: false,
  });
}
