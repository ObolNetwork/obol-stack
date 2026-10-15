import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { mapAgent8004ToItem } from './erc8004-source.ts';

const BASE_AGENT = {
  token_id: '42',
  chain_id: 8453,
  name: 'demo-quant',
  description: 'Agent-backed quant analyst.',
  image_url: 'https://example.com/quant.png',
  owner_address: '0x1111111111111111111111111111111111111111',
  x402_supported: true,
  supported_protocols: ['MCP', 'A2A'],
  services: {
    chat: { endpoint: 'https://example.com/services/chat', tools: ['plan'] },
    summarize: { endpoint: 'https://example.com/services/summarize' },
  },
  total_score: 87,
  star_count: 12,
  is_verified: true,
  health_status: null,
  tags: ['quant', 'on-chain'],
  categories: ['finance/analysis'],
  supported_trust_models: ['reputation'],
  created_tx_hash: '0xabc',
  is_testnet: false,
};

describe('mapAgent8004ToItem', () => {
  it('builds an Agent item from a complete 8004scan payload', () => {
    const item = mapAgent8004ToItem(BASE_AGENT);
    assert.equal(item.slug, 'erc8004/8453/42');
    assert.equal(item.name, 'demo-quant');
    assert.equal(item.namespace, 'erc8004:base');
    assert.equal(item.type, 'agent');
    assert.equal(item.modelOrRuntime, 'MCP, A2A');
    assert.deepEqual(item.skills, ['finance/analysis', 'quant', 'on-chain']);
    assert.equal(item.payTo, BASE_AGENT.owner_address);
    assert.equal(item.endpoint, 'https://example.com/services/chat');
    assert.equal(item.network.caip2, 'eip155:8453');
    assert.equal(item.network.alias, 'base');
    assert.equal(item.status, 'live');
    assert.equal(item.isDemo, false);
  });

  it('falls back to "agent-<id>" when name is empty', () => {
    const item = mapAgent8004ToItem({ ...BASE_AGENT, name: '' });
    assert.equal(item.name, 'agent-42');
  });

  it('maps base-sepolia chain id to the testnet alias', () => {
    const item = mapAgent8004ToItem({
      ...BASE_AGENT,
      chain_id: 84532,
      is_testnet: true,
    });
    assert.equal(item.network.alias, 'base-sepolia');
    assert.equal(item.namespace, 'erc8004:base-sepolia');
    assert.equal(item.isDemo, true);
  });

  it('classifies as http when no services or protocols are present', () => {
    const item = mapAgent8004ToItem({
      ...BASE_AGENT,
      services: null,
      supported_protocols: [],
    });
    assert.equal(item.type, 'http');
    assert.equal(item.endpoint, '');
  });

  it('renders modelOrRuntime as em-dash when no protocols are declared', () => {
    const item = mapAgent8004ToItem({
      ...BASE_AGENT,
      supported_protocols: [],
    });
    assert.equal(item.modelOrRuntime, '—');
  });

  it('flags health_status === "degraded" as the degraded status pill', () => {
    const item = mapAgent8004ToItem({
      ...BASE_AGENT,
      health_status: 'degraded',
    });
    assert.equal(item.status, 'degraded');
  });

  it('produces a deterministic banner seed per slug', () => {
    const a = mapAgent8004ToItem(BASE_AGENT);
    const b = mapAgent8004ToItem(BASE_AGENT);
    assert.equal(a.bannerSeed, b.bannerSeed);
    assert.match(a.bannerSeed, /^[0-9a-f]{12}$/);
  });

  it('uses eip155:<id> alias for chains not in the known set', () => {
    const item = mapAgent8004ToItem({ ...BASE_AGENT, chain_id: 999_999 });
    assert.equal(item.network.alias, 'eip155:999999');
    assert.equal(item.network.caip2, 'eip155:999999');
  });
});
