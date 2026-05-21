import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { filtersToSearchParams } from './query-params.ts';

describe('filtersToSearchParams', () => {
  it('omits empty fields', () => {
    const qs = filtersToSearchParams({}).toString();
    assert.equal(qs, '');
  });

  it('joins multi-value filters with comma', () => {
    const qs = filtersToSearchParams({
      type: ['agent', 'inference'],
      network: ['eip155:1', 'eip155:8453'],
      asset: ['USDC'],
    }).toString();
    const parsed = new URLSearchParams(qs);
    assert.equal(parsed.get('type'), 'agent,inference');
    assert.equal(parsed.get('network'), 'eip155:1,eip155:8453');
    assert.equal(parsed.get('asset'), 'USDC');
  });

  it('passes through scalar fields verbatim', () => {
    const qs = filtersToSearchParams({
      q: 'quant',
      sort: 'price_desc',
      minPrice: '0.5',
      maxPrice: '50',
      page: 3,
      pageSize: 50,
    });
    assert.equal(qs.get('q'), 'quant');
    assert.equal(qs.get('sort'), 'price_desc');
    assert.equal(qs.get('minPrice'), '0.5');
    assert.equal(qs.get('maxPrice'), '50');
    assert.equal(qs.get('page'), '3');
    assert.equal(qs.get('pageSize'), '50');
  });

  it('skips falsy scalar fields', () => {
    const qs = filtersToSearchParams({
      q: '',
      page: 0,
      pageSize: 0,
    });
    assert.equal(qs.toString(), '');
  });
});
