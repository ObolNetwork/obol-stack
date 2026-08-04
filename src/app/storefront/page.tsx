'use client';

import { useEffect, useState } from 'react';
import { BrandingEditorPage } from '@/components/storefront/BrandingEditorPage';

export default function StorefrontRoute() {
  const [hasMounted, setHasMounted] = useState(false);
  useEffect(() => {
    setHasMounted(true);
  }, []);
  if (!hasMounted) return null;
  return <BrandingEditorPage />;
}
