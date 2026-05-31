import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  AGENT_REGISTRATION_PATH,
  LOCAL_STOREFRONT_URL,
  normalizePublicBaseUrl,
  publicResourceUrl,
  publicServiceEndpoint,
  publicStorefrontUrl,
} from './public-storefront.ts';

describe('public storefront links', () => {
  it('normalizes tunnel base URLs', () => {
    assert.equal(
      normalizePublicBaseUrl('https://seller.trycloudflare.com/path///'),
      'https://seller.trycloudflare.com/path'
    );
  });

  it('falls back to the local controller storefront when no tunnel exists', () => {
    assert.equal(publicStorefrontUrl(null), LOCAL_STOREFRONT_URL);
    assert.equal(publicStorefrontUrl('  '), LOCAL_STOREFRONT_URL);
  });

  it('builds public resource URLs from either tunnel or local fallback', () => {
    assert.equal(
      publicResourceUrl(
        'https://seller.trycloudflare.com/',
        AGENT_REGISTRATION_PATH
      ),
      'https://seller.trycloudflare.com/.well-known/agent-registration.json'
    );
    assert.equal(
      publicResourceUrl(null, 'api/services.json'),
      'http://obol.stack:8080/api/services.json'
    );
  });

  it('builds service endpoints from either tunnel or local fallback', () => {
    assert.equal(
      publicServiceEndpoint('/services/demo-quant', null),
      'http://obol.stack:8080/services/demo-quant'
    );
    assert.equal(
      publicServiceEndpoint(
        'http://obol.stack:8080/services/demo-quant?count=1',
        null
      ),
      'http://obol.stack:8080/services/demo-quant?count=1'
    );
    assert.equal(
      publicServiceEndpoint(
        '/services/demo-quant?count=1',
        'https://seller.trycloudflare.com/public/'
      ),
      'https://seller.trycloudflare.com/public/services/demo-quant?count=1'
    );
  });
});
