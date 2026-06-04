import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { describe, it } from 'node:test';

describe('monitoring exposure', () => {
  it('does not expose the in-cluster monitoring hostname through a browser rewrite', async () => {
    const nextConfig = await readFile('next.config.ts', 'utf8');

    assert.doesNotMatch(nextConfig, /\/monitoring\/:path\*/);
    assert.doesNotMatch(nextConfig, /monitoring\.obol\.stack/);
  });

  it('keeps dashboard metrics on the server-side Prometheus client', async () => {
    const dashboardRoute = await readFile(
      'src/app/api/dashboard/route.ts',
      'utf8'
    );

    assert.match(
      dashboardRoute,
      /new PrometheusClient\(config\.prometheusUrl\)/
    );
  });
});
