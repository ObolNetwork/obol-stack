'use client';

import { useEffect, useState } from 'react';
import { StorefrontPage } from '@/components/storefront/StorefrontPage';

export default function StorefrontRoute() {
  const [hasMounted, setHasMounted] = useState(false);
  useEffect(() => {
    setHasMounted(true);
  }, []);
  if (!hasMounted) return null;
  return <StorefrontPage />;
}
