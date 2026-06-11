import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { buildServiceOfferBody } from './sell-create.ts';

describe('buildServiceOfferBody', () => {
  it('preserves the previous default HTTP ServiceOffer shape', () => {
    const body = buildServiceOfferBody({
      name: 'demo',
      namespace: 'llm',
      wallet: '0x1111111111111111111111111111111111111111',
      chain: 'base',
      pricePerRequest: '0.001',
      upstream: 'litellm',
      port: 4000,
      healthPath: '/health/readiness',
    });

    assert.equal(body.spec.type, 'http');
    assert.equal(body.spec.payment.network, 'base');
    assert.equal(body.spec.payment.price.perRequest, '0.001');
    assert.equal(body.spec.payment.asset, undefined);
  });

  it('can create an inference ServiceOffer with explicit asset metadata', () => {
    const body = buildServiceOfferBody({
      name: 'obol-demo',
      namespace: 'llm',
      type: 'inference',
      model: 'qwen3.5:4b',
      wallet: '0x1111111111111111111111111111111111111111',
      chain: 'mainnet',
      token: 'OBOL',
      asset: {
        address: '0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7',
        symbol: 'OBOL',
        decimals: 18,
        transferMethod: 'permit2',
        eip712Name: 'Obol Network',
        eip712Version: '1',
      },
      pricePerRequest: '0.5',
      upstream: 'obol-demo',
      port: 8402,
      healthPath: '/health',
    });

    assert.equal(body.spec.type, 'inference');
    assert.equal(body.spec.model.name, 'qwen3.5:4b');
    assert.equal(body.spec.model.runtime, 'ollama');
    assert.equal(body.spec.path, '/services/obol-demo');
    assert.equal(body.spec.upstream.port, 8402);
    assert.equal(body.spec.payment.network, 'mainnet');
    assert.equal(body.spec.payment.asset.symbol, 'OBOL');
    assert.equal(body.spec.payment.asset.transferMethod, 'permit2');
    assert.equal(body.spec.payment.asset.eip712Name, 'Obol Network');
    assert.equal(body.spec.payment.asset.eip712Version, '1');
  });
});
