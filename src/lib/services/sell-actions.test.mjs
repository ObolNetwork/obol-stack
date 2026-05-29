import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { serviceOfferResourcePath } from './sell-actions.ts';

describe('serviceOfferResourcePath', () => {
  it('builds the ServiceOffer resource API path', () => {
    assert.equal(
      serviceOfferResourcePath('demo-quant', 'agent-openclaw'),
      '/api/sell/demo-quant?ns=agent-openclaw'
    );
  });

  it('encodes name and namespace before placing them into the URL', () => {
    assert.equal(
      serviceOfferResourcePath('demo/quant', 'agent team'),
      '/api/sell/demo%2Fquant?ns=agent%20team'
    );
  });
});
