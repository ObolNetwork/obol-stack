'use client';

import { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';

import { MarketplaceDetail } from '@/components/marketplace/MarketplaceDetail';

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
  // Providers are installed once at the marketplace layout level.
  const raw = params?.slug;
  const slug = Array.isArray(raw) ? raw.join('/') : (raw ?? '');

  return (
    <MarketplaceDetail slug={slug} onBack={() => router.push('/marketplace')} />
  );
}
