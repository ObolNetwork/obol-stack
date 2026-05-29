import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { serviceOfferAssetSymbol } from './service-offer-display.ts';

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
