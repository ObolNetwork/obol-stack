'use client';

import { useEffect } from 'react';

import { StatusPanel } from '@/components/organisms/StatusPanel';
import { FallbackShell } from '@/components/templates/FallbackShell';

export default function RouteError({
  error,
  retry,
}: {
  error: Error & { digest?: string };
  retry: () => void;
}) {
  useEffect(() => {
    console.error('[dashboard] route error', error);
  }, [error]);

  return (
    <FallbackShell>
      <StatusPanel
        code="Error"
        title="Something went wrong"
        description={
          error.digest
            ? `The dashboard hit an unexpected error (ref ${error.digest}).`
            : 'The dashboard hit an unexpected error.'
        }
        onRetry={retry}
      />
    </FallbackShell>
  );
}
