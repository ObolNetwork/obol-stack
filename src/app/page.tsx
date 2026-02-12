'use client';
import { useEffect, useState } from 'react';
import { DashboardPage } from '../components';
import { Providers } from './providers';

export default function ObolStackDashboard() {
  const [hasMounted, setHasMounted] = useState(false);

  useEffect(() => {
    setHasMounted(true);
  }, []);

  if (!hasMounted) {
    return null;
  }
  return (
    <Providers>
      <DashboardPage />
    </Providers>
  );
}
