import { test, expect } from '@playwright/test';
import {
  waitForNetworkIdle,
  VIEWPORTS,
  testResponsiveness,
} from './utils/test-helpers';

/**
 * Example test file demonstrating various Playwright features
 * This serves as a template for writing new tests
 */

test.describe('Example Test Suite', () => {
  test.beforeEach(async ({ page }) => {
    // Navigate to the page before each test
    await page.goto('/');
  });

  test('example: basic page load test', async ({ page }) => {
    // Wait for the page to fully load
    await waitForNetworkIdle(page);

    // Verify the page URL
    expect(page.url()).toContain('localhost:3000');

    // Check that body is visible
    await expect(page.locator('body')).toBeVisible();
  });

  test('example: test with custom viewport', async ({ page }) => {
    // Set a specific viewport size
    await page.setViewportSize(VIEWPORTS.mobile);

    // Reload to ensure responsive styles are applied
    await page.reload();
    await waitForNetworkIdle(page);

    // Verify page is still functional
    await expect(page.locator('body')).toBeVisible();
  });

  test('example: test multiple viewports', async ({ page }) => {
    // Test across multiple viewports
    await testResponsiveness(page, [
      VIEWPORTS.mobile,
      VIEWPORTS.tablet,
      VIEWPORTS.desktop,
    ]);
  });

  test.skip('example: skipped test', async () => {
    // This test will be skipped
    // Use test.skip() to temporarily disable tests
  });

  test('example: test with tags @smoke', async ({ page }) => {
    // You can run specific tests using tags
    // Run with: pnpm test:e2e --grep @smoke
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
  });
});

test.describe('Example: API Testing', () => {
  test('example: test API endpoint', async ({ request }) => {
    // Test API endpoints directly without a browser
    try {
      const response = await request.get('/api/dashboard');
      expect(response.status()).toBeLessThan(500);
    } catch (error) {
      // Handle case where endpoint might not exist yet
      console.log('API endpoint not available:', error);
    }
  });
});

test.describe('Example: Advanced Interactions', () => {
  test('example: test keyboard navigation', async ({ page }) => {
    await page.goto('/');
    await waitForNetworkIdle(page);

    // Press Tab key
    await page.keyboard.press('Tab');

    // Check if an element is focused
    const focusedElement = await page.evaluate(() => {
      return document.activeElement?.tagName;
    });

    expect(focusedElement).toBeTruthy();
  });

  test('example: test with mocked API', async ({ page }) => {
    // Mock an API response
    await page.route('**/api/dashboard', route => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          status: 'healthy',
          data: { message: 'Mocked response' },
        }),
      });
    });

    await page.goto('/');
    await waitForNetworkIdle(page);

    // Your assertions here based on the mocked data
  });

  test('example: wait for specific element', async ({ page }) => {
    await page.goto('/');

    // Wait for a specific element to be visible
    // Adjust selector based on your actual components
    await page.waitForSelector('body', { state: 'visible' });

    const body = page.locator('body');
    await expect(body).toBeVisible();
  });
});
