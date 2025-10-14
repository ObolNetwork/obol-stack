import { Page, expect } from '@playwright/test';

/**
 * Wait for a specific element to be visible on the page
 */
export async function waitForElement(
  page: Page,
  selector: string,
  timeout = 5000
) {
  await page.waitForSelector(selector, { state: 'visible', timeout });
}

/**
 * Check if an element exists on the page
 */
export async function elementExists(page: Page, selector: string) {
  const element = page.locator(selector);
  return (await element.count()) > 0;
}

/**
 * Take a screenshot with a custom name
 */
export async function takeScreenshot(
  page: Page,
  name: string,
  options?: { fullPage?: boolean }
) {
  await page.screenshot({
    path: `test-results/screenshots/${name}.png`,
    fullPage: options?.fullPage ?? true,
  });
}

/**
 * Wait for all network activity to finish
 */
export async function waitForNetworkIdle(page: Page, timeout = 30000) {
  await page.waitForLoadState('networkidle', { timeout });
}

/**
 * Check console for errors
 */
export async function checkConsoleErrors(page: Page) {
  const errors: string[] = [];

  page.on('console', msg => {
    if (msg.type() === 'error') {
      errors.push(msg.text());
    }
  });

  page.on('pageerror', error => {
    errors.push(error.message);
  });

  return {
    getErrors: () => errors,
    expectNoErrors: () => expect(errors).toHaveLength(0),
  };
}

/**
 * Mock API response
 */
export async function mockApiResponse(
  page: Page,
  url: string | RegExp,
  response: Record<string, unknown>,
  status = 200
) {
  await page.route(url, route => {
    route.fulfill({
      status,
      contentType: 'application/json',
      body: JSON.stringify(response),
    });
  });
}

/**
 * Wait for a specific text to appear on the page
 */
export async function waitForText(page: Page, text: string, timeout = 5000) {
  await page.waitForSelector(`text=${text}`, { timeout });
}

/**
 * Fill a form field
 */
export async function fillField(page: Page, selector: string, value: string) {
  await page.fill(selector, value);
}

/**
 * Click an element and wait for navigation
 */
export async function clickAndNavigate(page: Page, selector: string) {
  await Promise.all([page.waitForNavigation(), page.click(selector)]);
}

/**
 * Get text content of an element
 */
export async function getTextContent(page: Page, selector: string) {
  return await page.textContent(selector);
}

/**
 * Check if page is responsive
 */
export async function testResponsiveness(
  page: Page,
  viewports: Array<{ width: number; height: number }>
) {
  for (const viewport of viewports) {
    await page.setViewportSize(viewport);
    await page.waitForLoadState('networkidle');

    const body = page.locator('body');
    await expect(body).toBeVisible();
  }
}

/**
 * Common viewport sizes for testing
 */
export const VIEWPORTS = {
  mobile: { width: 375, height: 667 },
  tablet: { width: 768, height: 1024 },
  desktop: { width: 1920, height: 1080 },
  smallDesktop: { width: 1366, height: 768 },
};
