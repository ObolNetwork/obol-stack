import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { offerKey, serviceOfferFromCatalog } from './sell-catalog.ts';

const CATALOG_OBOL_ADDRESS = '0x2222222222222222222222222222222222222222';

describe('serviceOfferFromCatalog', () => {
  it('maps catalog-only payment and network fields into ServiceOffer rows', () => {
    const offer = serviceOfferFromCatalog({
      name: 'alice-obol',
      namespace: 'llm',
      type: 'inference',
      model: 'qwen3.5',
      endpoint: 'https://seller.example/services/alice-obol',
      price: '0.5 OBOL/request',
      priceRaw: '0.5',
      priceUnit: 'perRequest',
      priceAtomicUnits: '500000000000000000',
      payTo: '0x1111111111111111111111111111111111111111',
      network: 'ethereum',
      caip2Network: 'eip155:1',
      chainId: 1,
      asset: {
        symbol: 'OBOL',
        address: CATALOG_OBOL_ADDRESS,
        decimals: 18,
        transferMethod: 'permit2',
        eip712Domain: { name: 'Obol Network', version: '1' },
      },
      description: 'Paid OBOL inference',
      isDemo: false,
      registrationPending: true,
      drainEndsAt: '2026-05-29T12:00:00Z',
    });

    assert.ok(offer);
    assert.equal(offer.name, 'alice-obol');
    assert.equal(offer.status, 'Ready');
    assert.equal(offer.priceRaw, '0.5');
    assert.equal(offer.priceAtomicUnits, '500000000000000000');
    assert.equal(offer.caip2Network, 'eip155:1');
    assert.equal(offer.chainId, 1);
    assert.equal(offer.payment.network, 'ethereum');
    assert.equal(offer.payment.asset.symbol, 'OBOL');
    assert.equal(offer.payment.asset.address, CATALOG_OBOL_ADDRESS);
    assert.equal(offer.payment.asset.transferMethod, 'permit2');
    assert.equal(offer.payment.asset.eip712Domain.name, 'Obol Network');
    assert.equal(offer.registrationPending, true);
    assert.equal(offer.drainEndsAt, '2026-05-29T12:00:00Z');
  });

  it('lets catalog fields win over divergent live CR payment fields', () => {
    const offer = serviceOfferFromCatalog(
      {
        name: 'alice-obol',
        namespace: 'llm',
        priceRaw: '0.5',
        priceUnit: 'perRequest',
        priceAtomicUnits: '500000000000000000',
        payTo: '0x1111111111111111111111111111111111111111',
        network: 'ethereum',
        caip2Network: 'eip155:1',
        asset: {
          symbol: 'OBOL',
          address: CATALOG_OBOL_ADDRESS,
          decimals: 18,
          transferMethod: 'permit2',
          eip712Domain: { name: 'Obol Network', version: '1' },
        },
      },
      {
        name: 'alice-obol',
        namespace: 'llm',
        type: 'http',
        status: 'Pending',
        payment: {
          network: 'ethereum',
          payTo: '0xffffffffffffffffffffffffffffffffffffffff',
          asset: {
            symbol: 'USDC',
            address: '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48',
            decimals: 6,
          },
          price: { perRequest: '999' },
        },
      }
    );

    assert.ok(offer);
    assert.equal(offer.status, 'Pending');
    assert.equal(offer.payment.network, 'ethereum');
    assert.equal(
      offer.payment.payTo,
      '0x1111111111111111111111111111111111111111'
    );
    assert.equal(offer.payment.price.perRequest, '0.5');
    assert.equal(offer.payment.asset.symbol, 'OBOL');
    assert.equal(offer.payment.asset.address, CATALOG_OBOL_ADDRESS);
    assert.equal(offer.payment.asset.transferMethod, 'permit2');
  });

  it('skips entries missing the catalog identity fields', () => {
    assert.equal(serviceOfferFromCatalog({ name: '', namespace: 'llm' }), null);
    assert.equal(serviceOfferFromCatalog({ name: 'x', namespace: '' }), null);
  });
});

describe('offerKey', () => {
  it('joins namespace and name', () => {
    assert.equal(offerKey('llm', 'alice-obol'), 'llm/alice-obol');
  });
});
