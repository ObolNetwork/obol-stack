import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  formatUsdcAmount,
  serviceOfferAssetSymbol,
  serviceOfferUsdcEquivalent,
} from './service-offer-display.ts';

describe('serviceOfferAssetSymbol', () => {
  it('preserves the payment asset symbol from the offer', () => {
    assert.equal(
      serviceOfferAssetSymbol({ asset: { symbol: 'OBOL' } }),
      'OBOL'
    );
  });

  it('does not invent USDC when the asset symbol is missing', () => {
    assert.equal(serviceOfferAssetSymbol({ asset: {} }), 'token');
    assert.equal(serviceOfferAssetSymbol(undefined), 'token');
  });

  it('treats blank symbols as missing', () => {
    assert.equal(
      serviceOfferAssetSymbol({ asset: { symbol: '   ' } }),
      'token'
    );
  });
});

describe('formatUsdcAmount', () => {
  it('uses two decimals for ordinary USDC amounts', () => {
    assert.equal(formatUsdcAmount(2.5), '2.50');
  });

  it('keeps useful precision for small USDC amounts', () => {
    assert.equal(formatUsdcAmount(0.00025), '0.00025');
  });
});

describe('serviceOfferUsdcEquivalent', () => {
  it('multiplies OBOL price by the live OBOL/USD price', () => {
    assert.equal(serviceOfferUsdcEquivalent('10', 'OBOL', 0.25), '2.50');
  });

  it('matches OBOL case-insensitively', () => {
    assert.equal(serviceOfferUsdcEquivalent('2', 'obol', 0.5), '1.00');
  });

  it('does not compute an equivalent for non-OBOL assets', () => {
    assert.equal(serviceOfferUsdcEquivalent('10', 'USDC', 0.25), null);
  });

  it('does not compute an equivalent without a valid live price', () => {
    assert.equal(serviceOfferUsdcEquivalent('10', 'OBOL', undefined), null);
    assert.equal(serviceOfferUsdcEquivalent('10', 'OBOL', 0), null);
    assert.equal(serviceOfferUsdcEquivalent('10', 'OBOL', -1), null);
    assert.equal(serviceOfferUsdcEquivalent('10', 'OBOL', Number.NaN), null);
  });

  it('does not compute an equivalent from a malformed price', () => {
    assert.equal(serviceOfferUsdcEquivalent('free', 'OBOL', 0.25), null);
  });
});
