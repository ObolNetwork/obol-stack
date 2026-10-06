import { DashboardPage } from '@/components/pages/DashboardPage';
import { Providers } from './providers';

/**
 * Server component: the dashboard shell (header, nav, loading skeleton,
 * footer) is server-rendered. Wallet-dependent UI mounts client-side via
 * <ClientOnly> inside the header, and the wagmi config is SSR-safe
 * (src/lib/config/wagmi.ts), so no blank first paint.
 */
export default function ObolStackDashboard() {
  return (
    <Providers>
      <DashboardPage />
    </Providers>
  );
}
