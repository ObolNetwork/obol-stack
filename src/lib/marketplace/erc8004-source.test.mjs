import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { mapAgentEventToItem } from './erc8004-source.ts';

const BASE_ARGS = {
  chainId: 1,
  chainAlias: 'mainnet',
  chainCaip2: 'eip155:1',
  agentId: 42n,
  agentURI: 'https://example.com/.well-known/agent-registration.json',
  owner: '0x1111111111111111111111111111111111111111',
};

describe('mapAgentEventToItem', () => {
  it('builds an Agent-typed item from a complete metadata doc', () => {
    const item = mapAgentEventToItem(BASE_ARGS, {
      name: 'demo-quant',
      description: 'Agent-backed quant analyst.',
      endpoint: 'https://example.com/services/demo-quant',
      registration: {
        skills: ['addresses', 'ethereum-networks'],
        metadata: {
          model: 'qwen3.5:9b',
          runtime: 'hermes',
          pricingUnit: 'agent-turn',
          x402Price: '10',
          x402Asset: 'OBOL',
          x402Network: 'ethereum',
        },
      },
    });
    assert.equal(item.slug, 'erc8004/1/42');
    assert.equal(item.name, 'demo-quant');
    assert.equal(item.namespace, 'erc8004:mainnet');
    assert.equal(item.type, 'agent');
    assert.equal(item.modelOrRuntime, 'hermes · qwen3.5:9b');
    assert.deepEqual(item.skills, ['addresses', 'ethereum-networks']);
    assert.equal(item.price, '10');
    assert.equal(item.priceUnit, 'agent-turn');
    assert.equal(item.asset.symbol, 'OBOL');
    assert.equal(item.network.caip2, 'eip155:1');
    assert.equal(item.network.chainId, 1);
    assert.equal(item.network.alias, 'ethereum');
    assert.equal(item.endpoint, 'https://example.com/services/demo-quant');
    assert.equal(item.payTo, BASE_ARGS.owner);
    assert.equal(item.status, 'live');
  });

  it('falls back to event-only fields when the doc is null', () => {
    const item = mapAgentEventToItem(BASE_ARGS, null);
    assert.equal(item.name, 'agent-42');
    assert.equal(item.type, 'http');
    assert.equal(item.modelOrRuntime, '—');
    assert.equal(item.status, 'pending-registration');
    assert.match(item.description, /On-chain ERC-8004 agent #42/);
    assert.equal(item.asset.symbol, 'USDC'); // default
    assert.equal(item.payTo, BASE_ARGS.owner);
  });

  it('keeps runtime-only metadata as Agent type', () => {
    const item = mapAgentEventToItem(BASE_ARGS, {
      registration: { metadata: { runtime: 'hermes' } },
    });
    assert.equal(item.type, 'agent');
    assert.equal(item.modelOrRuntime, 'hermes');
  });

  it('uppercases asset symbol from metadata', () => {
    const item = mapAgentEventToItem(BASE_ARGS, {
      registration: { metadata: { x402Asset: 'obol' } },
    });
    assert.equal(item.asset.symbol, 'OBOL');
  });

  it('uses services[0].endpoint when top-level endpoint is missing', () => {
    const item = mapAgentEventToItem(BASE_ARGS, {
      services: [{ name: 'chat', endpoint: 'https://nested.example/chat' }],
    });
    assert.equal(item.endpoint, 'https://nested.example/chat');
  });

  it('produces a deterministic banner seed per slug', () => {
    const a = mapAgentEventToItem(BASE_ARGS, null);
    const b = mapAgentEventToItem(BASE_ARGS, null);
    assert.equal(a.bannerSeed, b.bannerSeed);
    assert.match(a.bannerSeed, /^[0-9a-f]{12}$/);
  });
});
