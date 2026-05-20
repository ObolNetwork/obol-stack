'use client';

import { useEffect, useState } from 'react';

import { MarketplacePage } from '@/components/marketplace';
import { Providers } from '../providers';

export default function MarketplaceRoute() {
  // Mirror the gate `src/app/page.tsx` uses — wagmi/RainbowKit need the DOM
  // before hydration, so we delay rendering until mounted.
  const [hasMounted, setHasMounted] = useState(false);
  useEffect(() => {
    setHasMounted(true);
  }, []);
  if (!hasMounted) return null;
  return (
    <Providers>
      <MarketplacePage />
    </Providers>
  );
}
