import assert from 'node:assert/strict';
import { afterEach, describe, it } from 'node:test';

import { COINGECKO_IDS } from '../lib/constants/tokens.ts';
import {
  fetchTokenPrices,
  normalizeTokenPrices,
  TOKEN_PRICE_REFRESH_MS,
} from './useTokenPrices.ts';

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe('TOKEN_PRICE_REFRESH_MS', () => {
  it('refreshes token prices every five minutes', () => {
    assert.equal(TOKEN_PRICE_REFRESH_MS, 5 * 60 * 1000);
  });
});

describe('normalizeTokenPrices', () => {
  it('maps CoinGecko USD prices and 24h changes', () => {
    assert.deepEqual(
      normalizeTokenPrices({
        [COINGECKO_IDS.ETHEREUM]: { usd: 3201.5, usd_24h_change: 1.25 },
        [COINGECKO_IDS.OBOL]: { usd: 0.42, usd_24h_change: -2.5 },
      }),
      {
        ethereum: { price: 3201.5, change24h: 1.25 },
        obol: { price: 0.42, change24h: -2.5 },
      }
    );
  });

  it('does not invent hardcoded fallback prices', () => {
    assert.deepEqual(normalizeTokenPrices({}), {
      ethereum: { price: 0, change24h: 0 },
      obol: { price: 0, change24h: 0 },
    });
  });
});

describe('fetchTokenPrices', () => {
  it('requests the live CoinGecko quote and normalizes the response', async () => {
    let requestedUrl = '';
    globalThis.fetch = async url => {
      requestedUrl = String(url);
      return {
        ok: true,
        json: async () => ({
          [COINGECKO_IDS.ETHEREUM]: { usd: 3000, usd_24h_change: 1 },
          [COINGECKO_IDS.OBOL]: { usd: 0.25, usd_24h_change: 2 },
        }),
      };
    };

    assert.deepEqual(await fetchTokenPrices(), {
      ethereum: { price: 3000, change24h: 1 },
      obol: { price: 0.25, change24h: 2 },
    });
    assert.match(requestedUrl, /api\.coingecko\.com\/api\/v3\/simple\/price/);
    assert.match(requestedUrl, new RegExp(COINGECKO_IDS.OBOL));
    assert.match(requestedUrl, new RegExp(COINGECKO_IDS.ETHEREUM));
  });

  it('throws when the price API fails', async () => {
    globalThis.fetch = async () => ({
      ok: false,
      json: async () => ({}),
    });

    await assert.rejects(fetchTokenPrices(), /Failed to fetch token prices/);
  });
});
