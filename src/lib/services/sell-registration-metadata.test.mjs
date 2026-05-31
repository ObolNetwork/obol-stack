import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { serviceOfferFromCatalog } from './sell-catalog.ts';
import { normalizeServiceOffer } from './sell-normalize.ts';

describe('ServiceOffer registration metadata', () => {
  it('normalizes registration services, supported trust, metadata, and provenance', () => {
    const offer = normalizeServiceOffer({
      metadata: { name: 'research-api', namespace: 'agent-research' },
      spec: {
        type: 'http',
        provenance: {
          experiment: 'auto-research-42',
          score: '0.93',
          ignored: 123,
        },
        payment: { price: { perRequest: '1' } },
        registration: {
          enabled: true,
          services: [
            { name: 'web', endpoint: 'https://seller.example/services/api' },
            { name: 'MCP', version: '2026-01' },
            'ignored',
          ],
          supportedTrust: [
            'reputation',
            'crypto-economic',
            123,
            'tee-attestation',
          ],
          metadata: {
            license: 'MIT',
            dataset: 'public',
            ignored: false,
          },
        },
      },
    });

    assert.deepEqual(offer.registration?.services, [
      { name: 'web', endpoint: 'https://seller.example/services/api' },
      { name: 'MCP', version: '2026-01' },
    ]);
    assert.deepEqual(offer.registration?.supportedTrust, [
      'reputation',
      'crypto-economic',
      'tee-attestation',
    ]);
    assert.deepEqual(offer.registration?.metadata, {
      license: 'MIT',
      dataset: 'public',
    });
    assert.deepEqual(offer.provenance, {
      experiment: 'auto-research-42',
      score: '0.93',
    });
  });

  it('preserves live registration metadata when catalog rows enrich ServiceOffers', () => {
    const offer = serviceOfferFromCatalog(
      {
        name: 'research-api',
        namespace: 'agent-research',
        type: 'http',
        priceRaw: '1',
        priceUnit: 'perRequest',
      },
      {
        name: 'research-api',
        namespace: 'agent-research',
        type: 'http',
        registration: {
          enabled: true,
          supportedTrust: ['reputation'],
          metadata: { license: 'MIT' },
          services: [{ name: 'web', endpoint: 'https://seller.example/api' }],
        },
        provenance: { experiment: 'auto-research-42' },
      }
    );

    assert.deepEqual(offer?.registration?.supportedTrust, ['reputation']);
    assert.deepEqual(offer?.registration?.metadata, { license: 'MIT' });
    assert.deepEqual(offer?.registration?.services, [
      { name: 'web', endpoint: 'https://seller.example/api' },
    ]);
    assert.deepEqual(offer?.provenance, { experiment: 'auto-research-42' });
  });
});
