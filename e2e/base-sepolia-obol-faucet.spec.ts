import { test, expect } from '@playwright/test';

const dashboardFixture = {
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
    beaconApi: { port: 4000, status: 'closed' },
  },
};

test.describe('Base Sepolia OBOL faucet', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/api/dashboard', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(dashboardFixture),
      });
    });

    await page.route('**/api/config/base-sepolia-obol', async route => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          chainId: 84532,
          chainName: 'Base Sepolia',
          symbol: 'OBOL',
          decimals: 18,
          claimAmountLabel: '5 OBOL',
          tokenAddress: '0x1111111111111111111111111111111111111111',
          faucetAddress: '0x2222222222222222222222222222222222222222',
          explorerUrl: 'https://sepolia.basescan.org',
        }),
      });
    });
  });

  test('renders the faucet under the Base Sepolia network card', async ({
    page,
  }) => {
    await page.goto('/');

    const faucet = page.getByTestId('base-sepolia-obol-faucet');
    await expect(faucet).toBeVisible();
    await expect(faucet).toContainText('Base Sepolia OBOL Faucet');
    await expect(faucet).toContainText('Claim test OBOL for x402 smoke tests.');
    await expect(faucet).toContainText('Base Sepolia');

    await expect(
      faucet.getByRole('button', { name: 'Connect wallet to claim' })
    ).toBeDisabled();

    if (process.env.OBOL_FAUCET_SCREENSHOT_PATH) {
      await faucet.screenshot({
        path: process.env.OBOL_FAUCET_SCREENSHOT_PATH,
      });
    }
  });
});
