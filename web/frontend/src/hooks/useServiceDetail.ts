'use client';

import { useQuery } from '@tanstack/react-query';

import type { ServiceDetail } from '@/lib/marketplace/types';
import {
  STACK_UNREACHABLE,
  ServiceNotFoundError,
  StackUnreachableError,
} from '@/lib/marketplace/errors';

export function useServiceDetail(slug: string | undefined) {
  return useQuery<ServiceDetail, Error>({
    queryKey: ['marketplace', 'detail', slug],
    enabled: Boolean(slug),
    queryFn: async () => {
      let res: Response;
      try {
        res = await fetch(
          `/api/marketplace/services/${encodeURIComponent(slug as string)}`,
          { headers: { accept: 'application/json' } }
        );
      } catch (cause) {
        throw new StackUnreachableError('Obol Stack unreachable', 0, {
          cause,
        });
      }
      if (res.status === 404) {
        throw new ServiceNotFoundError();
      }
      if (!res.ok) {
        let body: { error?: string; code?: string } = {};
        try {
          body = (await res.json()) as { error?: string; code?: string };
        } catch {
          // ignore body-parse errors
        }
        if (body.code === STACK_UNREACHABLE || res.status >= 500) {
          throw new StackUnreachableError(
            body.error ?? `Obol Stack unreachable (HTTP ${res.status})`,
            res.status
          );
        }
        throw new Error(body.error ?? `HTTP ${res.status}`);
      }
      return (await res.json()) as ServiceDetail;
    },
    // A 404 is definitive; don't retry it three times before showing it.
    retry: (failureCount, error) =>
      !(error instanceof ServiceNotFoundError) && failureCount < 3,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
