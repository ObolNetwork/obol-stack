'use client';

import { StitchesRegistry } from './StitchesRegistry';
import { StatusPanel } from '@/components/organisms/StatusPanel';

/**
 * Last-resort boundary: replaces the root layout, so it brings its own
 * <html>/<body> and Stitches globals. Kept free of providers and data hooks.
 */
export default function GlobalError({
  error,
  retry,
}: {
  error: Error & { digest?: string };
  retry: () => void;
}) {
  return (
    <html lang="en">
      <body>
        <title>Error · Obol Stack</title>
        <StitchesRegistry>
          <StatusPanel
            code="Error"
            title="The dashboard failed to load"
            description={
              error.digest
                ? `An unexpected error occurred (ref ${error.digest}).`
                : 'An unexpected error occurred.'
            }
            onRetry={retry}
          />
        </StitchesRegistry>
      </body>
    </html>
  );
}
