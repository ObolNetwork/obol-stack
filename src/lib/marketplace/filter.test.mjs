import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

import { mapCatalog } from './catalog.ts';
import { computeFacets, filterItems, paginate, sortItems } from './filter.ts';

const here = dirname(fileURLToPath(import.meta.url));

async function loadFixture() {
  const raw = await readFile(
    resolve(here, '__fixtures__/services.json'),
    'utf8'
  );
  return mapCatalog(JSON.parse(raw));
}

describe('marketplace filterItems', () => {
  it('filters by type', async () => {
    const items = await loadFixture();
    const agents = filterItems(items, { type: ['agent'] });
    assert.equal(agents.length, 2);
    assert.ok(agents.every(it => it.type === 'agent'));
  });

  it('filters by network using CAIP-2', async () => {
    const items = await loadFixture();
    const base = filterItems(items, { network: ['eip155:8453'] });
    assert.equal(base.length, 1);
    assert.equal(base[0].name, 'legal-summarizer');
  });

  it('filters by asset symbol case-insensitively', async () => {
    const items = await loadFixture();
    const obol = filterItems(items, { asset: ['obol'] });
    assert.equal(obol.length, 1);
    assert.equal(obol[0].asset.symbol, 'OBOL');
  });

  it('applies min/max price as decimal numbers', async () => {
    const items = await loadFixture();
    const cheap = filterItems(items, { maxPrice: '1' });
    assert.deepEqual(cheap.map(it => it.name).sort(), [
      'ollama-gated',
      'rs-7b',
    ]);
    const pricey = filterItems(items, { minPrice: '2' });
    assert.deepEqual(pricey.map(it => it.name).sort(), [
      'demo-quant',
      'legal-summarizer',
    ]);
  });

  it('full-text q matches name, description, model, and skills', async () => {
    const items = await loadFixture();
    assert.equal(filterItems(items, { q: 'quant' }).length, 1);
    assert.equal(filterItems(items, { q: 'legal' }).length, 1);
    assert.equal(filterItems(items, { q: 'coder' }).length, 1);
    assert.equal(filterItems(items, { q: 'addresses' }).length, 1);
  });

  it('combines filters with AND semantics', async () => {
    const items = await loadFixture();
    const out = filterItems(items, {
      type: ['agent'],
      asset: ['OBOL'],
    });
    assert.equal(out.length, 1);
    assert.equal(out[0].name, 'demo-quant');
  });
});

describe('marketplace sortItems', () => {
  it('sorts by price ascending', async () => {
    const items = await loadFixture();
    const out = sortItems(items, 'price_asc');
    const prices = out.map(it => Number(it.price));
    for (let i = 1; i < prices.length; i++) {
      assert.ok(prices[i] >= prices[i - 1], `not sorted at ${i}: ${prices}`);
    }
  });

  it('sorts by price descending', async () => {
    const items = await loadFixture();
    const out = sortItems(items, 'price_desc');
    assert.equal(out[0].name, 'demo-quant');
  });

  it('sorts by name alphabetically', async () => {
    const items = await loadFixture();
    const out = sortItems(items, 'name');
    const names = out.map(it => it.name);
    assert.deepEqual(names, names.slice().sort());
  });

  it('recent puts live offers before pending-registration', async () => {
    const items = await loadFixture();
    const out = sortItems(items, 'recent');
    const firstPendingIdx = out.findIndex(
      it => it.status === 'pending-registration'
    );
    const lastLiveIdx = out.map(it => it.status).lastIndexOf('live');
    assert.ok(
      firstPendingIdx === -1 || firstPendingIdx > lastLiveIdx,
      'pending should not appear before live'
    );
  });
});

describe('marketplace paginate', () => {
  it('respects page/pageSize', async () => {
    const items = await loadFixture();
    const p1 = paginate(items, 1, 2);
    assert.equal(p1.items.length, 2);
    assert.equal(p1.total, items.length);
    assert.equal(p1.page, 1);
    const p2 = paginate(items, 2, 2);
    assert.equal(p2.items.length, 2);
    assert.equal(p2.page, 2);
    assert.notDeepEqual(p1.items[0].slug, p2.items[0].slug);
  });

  it('clamps page above last to last page', async () => {
    const items = await loadFixture();
    const out = paginate(items, 9999, 2);
    assert.equal(out.page, Math.ceil(items.length / 2));
  });
});

describe('marketplace computeFacets', () => {
  it('counts type/network/asset buckets', async () => {
    const items = await loadFixture();
    const f = computeFacets(items);
    const typeMap = Object.fromEntries(f.type.map(b => [b.value, b.count]));
    assert.equal(typeMap.agent, 2);
    assert.equal(typeMap.inference, 1);
    assert.equal(typeMap.http, 1);

    const networkMap = Object.fromEntries(
      f.network.map(b => [b.value, b.count])
    );
    assert.equal(networkMap['eip155:1'], 1);
    assert.equal(networkMap['eip155:84532'], 2);
    assert.equal(networkMap['eip155:8453'], 1);

    const assetMap = Object.fromEntries(f.asset.map(b => [b.value, b.count]));
    assert.equal(assetMap.USDC, 3);
    assert.equal(assetMap.OBOL, 1);
  });

  it('orders buckets by count then alphabetically', async () => {
    const items = await loadFixture();
    const f = computeFacets(items);
    for (let i = 1; i < f.network.length; i++) {
      assert.ok(f.network[i - 1].count >= f.network[i].count);
    }
  });
});
