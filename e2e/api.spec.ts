import { test, expect } from '@playwright/test';

const probeOrigin = 'http://obol.stack:3000';

// These tests hit the real API routes (no mocking).
// Without a live cluster the routes return 503 — that's correct behaviour.
// Tests validate response shape and error handling, not cluster-specific data.

test.describe('API routes', () => {
  test('GET /api/dashboard returns valid JSON with nodeOverview and ports', async ({
    request,
  }) => {
    const res = await request.get('/api/dashboard');

    // 200 (cluster up) or 503 (cluster down) are both correct
    expect([200, 503]).toContain(res.status());

    const body = await res.json();
    // Both success and degraded responses include these keys
    expect(body).toHaveProperty('nodeOverview');
    expect(body).toHaveProperty('ports');
  });

  test('GET /api/marketplace/services returns valid JSON', async ({
    request,
  }) => {
    const res = await request.get('/api/marketplace/services');

    expect([200, 503, 502]).toContain(res.status());

    const body = await res.json();
    // 200 has items/total; 503 has error key
    expect(typeof body).toBe('object');
    expect(body).not.toBeNull();
  });

  test('GET /api/agents/status without params returns 400', async ({
    request,
  }) => {
    const res = await request.get('/api/agents/status');

    expect(res.status()).toBe(400);

    const body = await res.json();
    expect(body).toHaveProperty('error');
  });

  test('GET /api/tunnel returns JSON with tunnelURL or error key', async ({
    request,
  }) => {
    const res = await request.get('/api/tunnel');

    expect(res.status()).toBeLessThan(600);

    const body = await res.json();
    const hasExpectedKey = 'tunnelURL' in body || 'error' in body;
    expect(hasExpectedKey).toBe(true);
  });

  test('GET /api/marketplace/services with filters returns valid JSON', async ({
    request,
  }) => {
    const res = await request.get(
      '/api/marketplace/services?type=inference&sort=price_asc&page=1&pageSize=10'
    );

    expect([200, 503, 502]).toContain(res.status());

    const body = await res.json();
    expect(typeof body).toBe('object');
  });

  test('POST /api/marketplace/probe without url returns 400', async ({
    request,
  }) => {
    const res = await request.post('/api/marketplace/probe', {
      headers: { Origin: probeOrigin },
      data: {},
    });

    expect(res.status()).toBe(400);

    const body = await res.json();
    expect(body).toHaveProperty('error');
  });

  test('POST /api/marketplace/probe rejects disallowed external URLs', async ({
    request,
  }) => {
    const res = await request.post('/api/marketplace/probe', {
      headers: { Origin: probeOrigin },
      data: {
        url: 'https://evil.example.com/services/foo',
        model: 'test-model',
      },
    });

    expect(res.status()).toBe(400);

    const body = await res.json();
    expect(body.error).toMatch(/Probe URL/i);
  });
});
