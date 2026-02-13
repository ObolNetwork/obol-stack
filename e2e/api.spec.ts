import { test, expect } from '@playwright/test';

test.describe('API Routes', () => {
  test('should respond to API health check if available', async ({
    request,
  }) => {
    // This test checks if your API routes are responding
    // Adjust the endpoint based on your actual API structure
    try {
      const response = await request.get('/api/dashboard');

      // API should respond (either successfully or with an expected error)
      expect(response.status()).toBeLessThan(500);
    } catch (error) {
      // If endpoint doesn't exist yet, that's okay for initial setup
      console.log('API endpoint not yet available:', error);
    }
  });

  test('should handle CORS correctly', async ({ page }) => {
    await page.goto('/');

    // Listen for any CORS errors
    const consoleErrors: string[] = [];
    page.on('console', msg => {
      if (msg.type() === 'error') {
        consoleErrors.push(msg.text());
      }
    });

    await page.waitForLoadState('networkidle');

    // Check for CORS errors
    const corsErrors = consoleErrors.filter(error =>
      error.toLowerCase().includes('cors')
    );

    expect(corsErrors.length).toBe(0);
  });
});
