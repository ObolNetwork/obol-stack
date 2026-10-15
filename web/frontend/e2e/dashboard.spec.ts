// Dashboard page tests.
//
// WHY MOCKS: The dashboard calls /api/dashboard which requires a live Kubernetes
// cluster (kubeconfig). In CI there is no cluster, so the real endpoint returns
// 503. We mock it so tests exercise the rendering logic (DISK/MEMORY/CPU/UPTIME,
// error states, loading state) regardless of cluster availability.
//
// IF A TEST BREAKS: Check whether the component's text labels or element roles
// changed. Key touchpoints: DashboardPage.tsx (hasMounted gate, loading/error
// conditions), NodeOverview.tsx (metric labels), StackUnreachableState.tsx
// (role="alert"), NavigatedDashboardHeader (nav uses <button>, not <a>).
import { test, expect } from '@playwright/test';

const DASHBOARD_DATA = {
  localNodes: [],
  erpcData: {
    networks: [{ id: 'evm:84532', alias: 'base-sepolia', state: 'OK' }],
    upstreams: [],
    status: 'ok',
    networkCount: 1,
    upstreamCount: 0,
  },
  nodeOverview: {
    diskUsage: { value: 10, total: 100, percentage: 10, unit: 'GB' },
    memoryUsage: { value: 4, total: 16, percentage: 25, unit: 'GB' },
    cpuLoad: { percentage: 12 },
    uptime: { hours: 42, status: 'healthy' },
  },
  ports: {
    p2p: { port: 30303, status: 'closed' },
    jsonRpc: { port: 8545, status: 'open' },
    prometheus: { port: 9090, status: 'open' },
    beaconApi: { port: 5052, status: 'closed' },
  },
};

test.describe('Dashboard page', () => {
  test('renders metric cards when cluster is reachable', async ({ page }) => {
    await page.route('**/api/dashboard', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(DASHBOARD_DATA),
      })
    );

    await page.goto('/');

    await expect(page.getByText('DISK')).toBeVisible();
    await expect(page.getByText('MEMORY')).toBeVisible();
    await expect(page.getByText('CPU')).toBeVisible();
    await expect(page.getByText('UPTIME')).toBeVisible();
  });

  test('shows unreachable state when cluster returns 503', async ({ page }) => {
    await page.route('**/api/dashboard', route =>
      route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'Obol Stack unreachable' }),
      })
    );

    await page.goto('/');

    const alert = page
      .getByRole('alert')
      .filter({ hasText: 'Obol Stack unreachable' });
    await expect(alert).toBeVisible();
    await expect(alert).toContainText('Obol Stack unreachable');
  });

  test('shows loading state while API is pending', async ({ page }) => {
    await page.route('**/api/dashboard', () => {
      // Never respond — holds the request open so loading state stays visible
    });

    await page.goto('/');

    await expect(page.getByText('Loading dashboard data...')).toBeVisible();
  });

  test('navigation header renders with Marketplace link', async ({ page }) => {
    await page.route('**/api/dashboard', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(DASHBOARD_DATA),
      })
    );

    await page.goto('/');

    // Nav uses <button> elements for routing, not <a> links
    await expect(
      page.getByRole('button', { name: /Marketplace/ })
    ).toBeVisible();
  });
});
