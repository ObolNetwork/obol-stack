import { StatusPanel } from '@/components/organisms/StatusPanel';

// Rendered inside the marketplace layout, which already supplies the shell.
export default function MarketplaceNotFound() {
  return (
    <StatusPanel
      code="404"
      title="Service not found"
      description="No marketplace listing matches this address. It may have been delisted or never existed."
      homeHref="/marketplace"
      homeLabel="Browse marketplace"
    />
  );
}
