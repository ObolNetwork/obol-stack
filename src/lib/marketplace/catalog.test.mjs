import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

import { fetchCatalog, mapCatalog } from './catalog.ts';

const here = dirname(fileURLToPath(import.meta.url));

async function loadRaw() {
  const raw = await readFile(
    resolve(here, '__fixtures__/services.json'),
    'utf8'
  );
  return JSON.parse(raw);
}

describe('mapCatalog', () => {
  it('returns [] on non-array input', () => {
    assert.deepEqual(mapCatalog(null), []);
    assert.deepEqual(mapCatalog({}), []);
    assert.deepEqual(mapCatalog('nope'), []);
  });

  it('skips entries without name+namespace', () => {
    const out = mapCatalog([
      { name: '', namespace: 'x' },
      { name: 'x', namespace: '' },
      {},
    ]);
    assert.equal(out.length, 0);
  });

  it('maps agent entries with runtime · model and dedupes skills', async () => {
    const raw = await loadRaw();
    const out = mapCatalog(raw);
    const demo = out.find(it => it.name === 'demo-quant');
    assert.ok(demo);
    assert.equal(demo.type, 'agent');
    assert.equal(demo.modelOrRuntime, 'hermes · qwen3.5:9b');
    // agentSkills and skills overlap on "addresses" — dedupe should leave 2.
    assert.deepEqual(demo.skills.sort(), ['addresses', 'ethereum-networks']);
    assert.equal(demo.status, 'pending-registration');
    assert.equal(demo.priceUnit, 'agent-turn');
    assert.equal(demo.price, '10');
    assert.equal(demo.asset.symbol, 'OBOL');
    assert.equal(
      demo.asset.address,
      raw.find(it => it.name === 'demo-quant').asset.address
    );
    assert.equal(demo.asset.decimals, 18);
    assert.equal(demo.asset.transferMethod, 'permit2');
    assert.equal(demo.asset.eip712Domain.name, 'Obol Network');
    assert.equal(demo.asset.eip712Domain.version, '1');
    assert.equal(demo.network.caip2, 'eip155:1');
    assert.equal(demo.network.chainId, 1);
  });

  it('maps inference entries using spec.model', async () => {
    const raw = await loadRaw();
    const out = mapCatalog(raw);
    const rs = out.find(it => it.name === 'rs-7b');
    assert.ok(rs);
    assert.equal(rs.type, 'inference');
    assert.equal(rs.modelOrRuntime, 'qwen2.5-coder');
    assert.equal(rs.status, 'live');
    assert.equal(rs.priceUnit, 'request');
  });

  it('prefers numeric priceRaw over display price and normalizes backend price units', () => {
    const [item] = mapCatalog([
      {
        name: 'live-price',
        namespace: 'llm',
        type: 'inference',
        model: 'qwen3.5:9b',
        endpoint: 'https://example.com/services/live-price',
        price: '10 OBOL/request',
        priceRaw: '10',
        priceUnit: 'perRequest',
        asset: { symbol: 'OBOL', decimals: 18 },
      },
    ]);
    assert.equal(item.price, '10');
    assert.equal(item.priceUnit, 'request');

    const [mtok] = mapCatalog([
      {
        name: 'mtok',
        namespace: 'llm',
        type: 'inference',
        priceRaw: '0.5',
        priceUnit: 'perMTok',
      },
    ]);
    assert.equal(mtok.priceUnit, 'mtok');
  });

  it('normalizes display-only prices when priceRaw is absent', () => {
    const [item] = mapCatalog([
      {
        name: 'display-only',
        namespace: 'llm',
        price: '10 OBOL/request',
        priceUnit: 'perRequest',
      },
    ]);
    assert.equal(item.price, '10');
    assert.equal(item.priceUnit, 'request');
  });

  it('derives slug and a stable bannerSeed', async () => {
    const raw = await loadRaw();
    const out = mapCatalog(raw);
    const demo = out.find(it => it.name === 'demo-quant');
    assert.equal(demo.slug, 'agent-demo-quant/demo-quant');
    assert.match(demo.bannerSeed, /^[0-9a-f]{12}$/);
    // Idempotent across runs of the same input.
    const out2 = mapCatalog(raw);
    assert.equal(
      out2.find(it => it.name === 'demo-quant').bannerSeed,
      demo.bannerSeed
    );
  });

  it('falls back to caip2 alias when network alias is empty', () => {
    const out = mapCatalog([
      {
        name: 'x',
        namespace: 'y',
        type: 'http',
        caip2Network: 'eip155:42',
        endpoint: 'http://x',
        price: '0',
        asset: { symbol: 'USDC', decimals: 6, address: '0xa' },
      },
    ]);
    assert.equal(out[0].network.caip2, 'eip155:42');
    assert.equal(out[0].network.chainId, 42);
  });

  it('flags demo entries', async () => {
    const raw = await loadRaw();
    const out = mapCatalog(raw);
    const ollama = out.find(it => it.name === 'ollama-gated');
    assert.equal(ollama.isDemo, true);
  });

  it('preserves drainEndsAt from the catalog', async () => {
    const raw = await loadRaw();
    const out = mapCatalog(raw);
    const legal = out.find(it => it.name === 'legal-summarizer');
    assert.equal(legal.drainEndsAt, '2026-05-29T12:00:00Z');
  });
});

describe('fetchCatalog', () => {
  it('returns an empty catalog when the default obol-skill-md service is absent', async () => {
    const originalFetch = globalThis.fetch;
    try {
      globalThis.fetch = async () => {
        const cause = Object.assign(new Error('getaddrinfo ENOTFOUND'), {
          code: 'ENOTFOUND',
          hostname: 'obol-skill-md.x402.svc.cluster.local',
        });
        throw new TypeError('fetch failed', { cause });
      };

      const out = await fetchCatalog();
      assert.deepEqual(out.items, []);
      assert.match(out.fetchedAt, /^\d{4}-\d{2}-\d{2}T/);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('still treats custom catalog URL failures as unreachable', async () => {
    const originalFetch = globalThis.fetch;
    try {
      globalThis.fetch = async () => {
        throw new TypeError('fetch failed');
      };

      await assert.rejects(
        () => fetchCatalog('http://custom.invalid/api/services.json'),
        /Obol Stack unreachable/
      );
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
