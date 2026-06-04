import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { serviceOfferFromCatalog } from './sell-catalog.ts';
import { normalizeServiceOffer } from './sell-normalize.ts';
import { linkOfferToAgent } from './services-join.ts';

const quantAgent = {
  id: 'quant',
  runtime: 'hermes',
  namespace: 'agent-quant',
  hostname: 'agent-quant.local',
  status: 'running',
};

describe('ServiceOffer agent references', () => {
  it('normalizes spec.agent.ref from raw ServiceOffer CRs', () => {
    const offer = normalizeServiceOffer({
      metadata: { name: 'quant-turn', namespace: 'agent-quant' },
      spec: {
        type: 'agent',
        agent: { ref: { name: 'quant', namespace: 'agent-quant' } },
        payment: { price: { perRequest: '10' } },
      },
      status: { conditions: [{ type: 'Ready', status: 'True' }] },
    });

    assert.deepEqual(offer.agent, {
      ref: { name: 'quant', namespace: 'agent-quant' },
    });
  });

  it('keeps the live agent reference when catalog rows enrich ServiceOffers', () => {
    const offer = serviceOfferFromCatalog(
      {
        name: 'quant-turn',
        namespace: 'agent-quant',
        type: 'agent',
        priceRaw: '10',
        priceUnit: 'agent-turn',
      },
      {
        name: 'quant-turn',
        namespace: 'agent-quant',
        type: 'agent',
        status: 'Ready',
        agent: { ref: { name: 'quant', namespace: 'agent-quant' } },
      }
    );

    assert.equal(offer?.agent?.ref?.name, 'quant');
    assert.equal(offer?.agent?.ref?.namespace, 'agent-quant');
  });

  it('links type=agent offers through spec.agent.ref before upstream namespace', () => {
    const link = linkOfferToAgent(
      {
        name: 'quant-turn',
        namespace: 'agent-quant',
        type: 'agent',
        agent: { ref: { name: 'quant', namespace: 'agent-quant' } },
        upstream: { namespace: 'llm' },
      },
      [
        { ...quantAgent },
        {
          id: 'shared-llm',
          runtime: 'hermes',
          namespace: 'llm',
          hostname: 'llm.local',
          status: 'running',
        },
      ]
    );

    assert.deepEqual(link, {
      runtime: 'hermes',
      id: 'quant',
      namespace: 'agent-quant',
    });
  });
});
