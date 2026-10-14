import type { Metadata } from 'next';

import { StatusPanel } from '@/components/organisms/StatusPanel';
import { FallbackShell } from '@/components/templates/FallbackShell';

export const metadata: Metadata = {
  title: 'Page not found · Obol Stack',
};

export default function NotFound() {
  return (
    <FallbackShell>
      <StatusPanel
        code="404"
        title="Page not found"
        description="This page doesn't exist on your Obol Stack dashboard."
      />
    </FallbackShell>
  );
}
