import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  attachAgentListings,
  buildAgentListing,
  findAgentOffer,
  offerAnchorId,
  offerMatchesAgent,
  splitPendingOffers,
} from './agent-listing.ts';

const subAgent = {
  id: 'quant',
  runtime: 'hermes',
  namespace: 'agent-quant',
  hostname: 'agent-quant.obol.stack',
  status: 'running',
  isSubAgent: true,
};

const agentOffer = {
  name: 'quant',
  namespace: 'agent-quant',
  type: 'agent',
  status: 'False',
  agent: { ref: { name: 'quant', namespace: 'agent-quant' } },
  conditions: [{ type: 'Ready', status: 'False', message: 'waiting' }],
};

describe('offerMatchesAgent', () => {
  it('matches by agent.ref namespace and name', () => {
    assert.equal(offerMatchesAgent(agentOffer, subAgent), true);
  });
  it('rejects inference offers', () => {
    assert.equal(
      offerMatchesAgent({ ...agentOffer, type: 'inference' }, subAgent),
      false
    );
  });
});

describe('findAgentOffer', () => {
  it('finds matching agent offer', () => {
    assert.equal(findAgentOffer(subAgent, [agentOffer])?.name, 'quant');
  });
  it('returns null for primary agents', () => {
    assert.equal(
      findAgentOffer({ ...subAgent, isSubAgent: false }, [agentOffer]),
      null
    );
  });
});

describe('buildAgentListing', () => {
  it('marks setting_up when not in catalog', () => {
    const listing = buildAgentListing(agentOffer, false);
    assert.equal(listing.phase, 'setting_up');
    assert.equal(listing.anchorId, 'offer-agent-quant-quant');
  });
  it('marks live when in catalog and operationally ready', () => {
    const live = {
      ...agentOffer,
      endpoint: 'http://x/services/quant',
      status: 'Ready',
      conditions: [
        { type: 'ModelReady', status: 'True', message: '' },
        { type: 'UpstreamHealthy', status: 'True', message: '' },
        { type: 'PaymentGateReady', status: 'True', message: '' },
        { type: 'RoutePublished', status: 'True', message: '' },
      ],
    };
    const listing = buildAgentListing(live, true);
    assert.equal(listing.phase, 'live');
  });
});

describe('splitPendingOffers', () => {
  it('returns agent CRs absent from catalog keys', () => {
    const catalogKeys = new Set(['llm/other']);
    const pending = splitPendingOffers(
      [agentOffer, { ...agentOffer, name: 'other', namespace: 'llm' }],
      catalogKeys
    );
    assert.equal(pending.length, 1);
    assert.equal(pending[0].name, 'quant');
  });
});

describe('attachAgentListings', () => {
  it('joins listing onto sub-agents', () => {
    const out = attachAgentListings([subAgent], [agentOffer], new Set());
    assert.equal(out[0].listing?.phase, 'setting_up');
  });
});

describe('offerAnchorId', () => {
  it('sanitizes slashes', () => {
    assert.equal(offerAnchorId('ns/sub', 'name'), 'offer-ns_sub-name');
  });
});
