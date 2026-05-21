'use client';

import { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';

import { MarketplaceDetail } from '@/components/marketplace/MarketplaceDetail';
import { Providers } from '@/app/providers';

export default function MarketplaceServiceRoute() {
  const params = useParams<{ slug: string | string[] }>();
  const router = useRouter();
  const [hasMounted, setHasMounted] = useState(false);

  useEffect(() => {
    setHasMounted(true);
  }, []);
  if (!hasMounted) return null;

  // Next encodes `/` in slugs, so a "ns/name" pair arrives as "ns%2Fname".
  // useParams decodes once, so we get "ns/name" back as a single string here.
  const raw = params?.slug;
  const slug = Array.isArray(raw) ? raw.join('/') : (raw ?? '');

  return (
    <Providers>
      <MarketplaceDetail
        slug={slug}
        onBack={() => router.push('/marketplace')}
      />
    </Providers>
  );
}
