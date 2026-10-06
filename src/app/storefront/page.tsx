import { ClientOnly } from '@/components/atoms/ClientOnly';
import { LoadingState } from '@/components/molecules/LoadingState';
import { BrandingEditorPage } from '@/components/storefront/BrandingEditorPage';

// The editor derives its preview origin from window.location, so it mounts
// client-side; the dashboard shell around it still server-renders.
export default function StorefrontRoute() {
  return (
    <ClientOnly
      fallback={<LoadingState message="Loading storefront editor…" />}
    >
      <BrandingEditorPage />
    </ClientOnly>
  );
}
