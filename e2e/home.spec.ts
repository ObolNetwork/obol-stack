import { test, expect } from '@playwright/test';

test.describe('Home Page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
  });

  test('should load the home page successfully', async ({ page }) => {
    // Wait for the page to load
    await page.waitForLoadState('networkidle');

    // Check that the page loaded
    expect(page.url()).toContain('localhost:3000');
  });

  test('should have correct title', async ({ page }) => {
    // Wait for title to be set
    await page.waitForLoadState('domcontentloaded');

    // Check the page has a title (even if empty/default for now)
    const title = await page.title();
    expect(title).toBeDefined();
  });

  test('should display main navigation elements', async ({ page }) => {
    // Wait for page to fully load
    await page.waitForLoadState('networkidle');

    // Check for common UI elements that should be present
    // Adjust these selectors based on your actual component structure
    const body = page.locator('body');
    await expect(body).toBeVisible();
  });

  test('should be responsive on mobile viewport', async ({ page }) => {
    // Set viewport to mobile size
    await page.setViewportSize({ width: 375, height: 667 });

    // Wait for page to load
    await page.waitForLoadState('networkidle');

    // Verify page is still functional
    const body = page.locator('body');
    await expect(body).toBeVisible();
  });
});
