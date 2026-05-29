import assert from 'node:assert/strict';
import { afterEach, describe, it } from 'node:test';

import { COINGECKO_IDS } from '../lib/constants/tokens.ts';
import {
  fetchTokenPrices,
  formatUsdcAmount,
  isObolSymbol,
  normalizeTokenPrices,
  obolAmountToUsdc,
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

describe('isObolSymbol', () => {
  it('matches OBOL case-insensitively', () => {
    assert.equal(isObolSymbol('OBOL'), true);
    assert.equal(isObolSymbol(' obol '), true);
  });

  it('rejects non-OBOL symbols', () => {
    assert.equal(isObolSymbol('USDC'), false);
    assert.equal(isObolSymbol(undefined), false);
  });
});

describe('formatUsdcAmount', () => {
  it('uses two decimals for ordinary USDC amounts', () => {
    assert.equal(formatUsdcAmount(2.5), '2.50');
    assert.equal(formatUsdcAmount(1234.5), '1,234.50');
  });

  it('keeps useful precision for small USDC amounts', () => {
    assert.equal(formatUsdcAmount(0.00025), '0.00025');
  });
});

describe('obolAmountToUsdc', () => {
  it('multiplies an OBOL amount by the live OBOL/USD price', () => {
    assert.equal(obolAmountToUsdc('10', 0.25), '2.50');
  });

  it('accepts comma-formatted display amounts', () => {
    assert.equal(obolAmountToUsdc('1,000', 0.25), '250.00');
  });

  it('does not compute an equivalent without a valid live price', () => {
    assert.equal(obolAmountToUsdc('10', undefined), null);
    assert.equal(obolAmountToUsdc('10', 0), null);
    assert.equal(obolAmountToUsdc('10', -1), null);
    assert.equal(obolAmountToUsdc('10', Number.NaN), null);
  });

  it('does not compute an equivalent from a malformed amount', () => {
    assert.equal(obolAmountToUsdc('free', 0.25), null);
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
