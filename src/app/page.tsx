'use client';
import { useEffect, useState } from 'react';
import { DashboardPage } from '../components';
import { Providers } from './providers';
import { CopilotKit } from '@copilotkit/react-core';
import '@copilotkit/react-ui/styles.css';

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
      <CopilotKit runtimeUrl="/api/copilotkit" agent="obol_agent">
        <DashboardPage />
      </CopilotKit>
    </Providers>
  );
}
