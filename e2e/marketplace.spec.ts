// Marketplace page tests.
//
// WHY MOCKS: /api/marketplace/services proxies to the in-cluster service catalog.
// Without a cluster it returns 503. We mock it to test rendering (service count,
// empty state, error state, tabs, navigation) in CI without a cluster.
//
// IF A TEST BREAKS: Key touchpoints:
//   - MarketplacePage.tsx — service count renders as `<strong>{total}</strong> services`
//     (split elements → use toContainText not getByText)
//   - ServicesSubNav.tsx — Browse/My Purchases/My Listings are <a> links
//   - StackUnreachableState.tsx — role="alert" (filter by text to avoid Next.js
//     route announcer which also has role="alert")
//   - hasMounted gate in src/app/marketplace/page.tsx delays render until hydration
import { test, expect } from '@playwright/test';

// items:[] is intentional — the page derives the count from `total`, not items.length.
// Passing actual item objects risks crashing the render if the fixture is missing
// fields the component requires; an empty array exercises the count display safely.
const SERVICES_RESPONSE = {
  items: [],
  total: 2,
  facets: {
    type: [{ value: 'inference', label: 'Inference', count: 2 }],
    network: [{ value: 'eip155:1', label: 'Ethereum', count: 2 }],
    asset: [{ value: 'OBOL', label: 'OBOL', count: 2 }],
    source: [{ value: 'remote', label: 'Remote', count: 2 }],
  },
  cachedAt: 0,
};

const EMPTY_SERVICES_RESPONSE = {
  items: [],
  total: 0,
  facets: { type: [], network: [], asset: [], source: [] },
  cachedAt: 0,
};

test.describe('Marketplace page', () => {
  test('shows service count from catalog', async ({ page }) => {
    await page.route('**/api/marketplace/services**', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(SERVICES_RESPONSE),
      })
    );

    await page.goto('/marketplace');

    // "2" is in a <strong>, "services" is a sibling text node — use toContainText
    await expect(page.locator('body')).toContainText('2 services');
  });

  test('shows zero count when catalog is empty', async ({ page }) => {
    await page.route('**/api/marketplace/services**', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(EMPTY_SERVICES_RESPONSE),
      })
    );

    await page.goto('/marketplace');

    await expect(page.locator('body')).toContainText('0 services');
  });

  test('shows unreachable state on 503', async ({ page }) => {
    await page.route('**/api/marketplace/services**', route =>
      route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'Obol Stack unreachable',
          code: 'STACK_UNREACHABLE',
        }),
      })
    );

    await page.goto('/marketplace');

    const alert = page
      .getByRole('alert')
      .filter({ hasText: 'Obol Stack unreachable' });
    await expect(alert).toBeVisible();
    await expect(alert).toContainText('Obol Stack unreachable');
  });

  test('navigation tabs are present', async ({ page }) => {
    await page.route('**/api/marketplace/services**', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(SERVICES_RESPONSE),
      })
    );

    await page.goto('/marketplace');

    await expect(page.getByRole('link', { name: /Browse/ })).toBeVisible();
    await expect(
      page.getByRole('link', { name: /My Purchases/ })
    ).toBeVisible();
    await expect(page.getByRole('link', { name: /My Listings/ })).toBeVisible();
  });

  test('navigates to listings sub-page', async ({ page }) => {
    await page.route('**/api/marketplace/services**', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(SERVICES_RESPONSE),
      })
    );

    await page.goto('/marketplace');
    await page.getByRole('link', { name: 'My Listings' }).click();

    await expect(page).toHaveURL(/\/marketplace\/listings/);
  });
});
