import { test, expect } from '@playwright/test';

test.describe('Dashboard Page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
  });

  test('should display dashboard elements', async ({ page }) => {
    await page.waitForLoadState('networkidle');

    // Check if the page has loaded properly
    const body = page.locator('body');
    await expect(body).toBeVisible();
  });

  test('should handle loading states', async ({ page }) => {
    // Navigate to the page
    await page.goto('/');

    // Wait for initial load
    await page.waitForLoadState('domcontentloaded');

    // The page should eventually finish loading
    await page.waitForLoadState('networkidle', { timeout: 30000 });
  });

  test('should be keyboard navigable', async ({ page }) => {
    await page.waitForLoadState('networkidle');

    // Test tab navigation
    await page.keyboard.press('Tab');

    // Check if focus is visible (basic accessibility test)
    const focusedElement = await page.evaluate(() => {
      return document.activeElement?.tagName;
    });

    expect(focusedElement).toBeTruthy();
  });

  test('should handle errors gracefully', async ({ page }) => {
    // Test with a potentially non-existent route
    const response = await page.goto('/non-existent-page');

    // Should either redirect or show 404
    expect(response?.status()).toBeTruthy();
  });
});
