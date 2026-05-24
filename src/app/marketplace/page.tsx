'use client';

import { useEffect, useState } from 'react';

import { MarketplacePage } from '@/components/marketplace';

export default function MarketplaceRoute() {
  // Mirror the gate `src/app/page.tsx` uses — wagmi/RainbowKit need the DOM
  // before hydration, so we delay rendering until mounted. Providers are
  // installed once at the marketplace layout level.
  const [hasMounted, setHasMounted] = useState(false);
  useEffect(() => {
    setHasMounted(true);
  }, []);
  if (!hasMounted) return null;
  return <MarketplacePage />;
}
