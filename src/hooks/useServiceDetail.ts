'use client';

import { useQuery } from '@tanstack/react-query';

import type { ServiceDetail } from '@/lib/marketplace/types';

export function useServiceDetail(slug: string | undefined) {
  return useQuery<ServiceDetail, Error>({
    queryKey: ['marketplace', 'detail', slug],
    enabled: Boolean(slug),
    queryFn: async () => {
      const res = await fetch(
        `/api/marketplace/services/${encodeURIComponent(slug as string)}`,
        { headers: { accept: 'application/json' } }
      );
      if (res.status === 404) {
        throw new Error('Service not found in catalog snapshot');
      }
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      return (await res.json()) as ServiceDetail;
    },
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
