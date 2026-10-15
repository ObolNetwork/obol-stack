'use client';

import { useRouter } from 'next/navigation';

import { MarketplaceDetail } from '@/components/marketplace/MarketplaceDetail';

export function MarketplaceDetailRoute({ slug }: { slug: string }) {
  const router = useRouter();
  return (
    <MarketplaceDetail slug={slug} onBack={() => router.push('/marketplace')} />
  );
}
