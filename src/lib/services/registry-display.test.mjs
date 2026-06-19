import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  chainIdForRegistryFilter,
  registryCapabilityBadges,
  registryChainBadges,
  registryChainLabel,
  registryHealthBadge,
} from './registry-display.ts';

const BASE_AGENT = {
  token_id: '7',
  chain_id: 84532,
  name: 'Quant Agent',
  description: 'Research agent',
  image_url: '',
  owner_address: '0x1111111111111111111111111111111111111111',
  x402_supported: true,
  supported_protocols: ['MCP'],
  services: {
    mcp: { endpoint: 'https://example.test/mcp', tools: ['portfolio'] },
    web: { endpoint: 'https://example.test', tools: ['quote'] },
  },
  total_score: 42,
  star_count: 5,
  is_verified: true,
  health_status: 'healthy',
  tags: ['quant', 'on-chain'],
  categories: ['finance'],
  supported_trust_models: ['feedback'],
  created_tx_hash: '0xabc',
  is_testnet: true,
};

describe('registry display helpers', () => {
  it('maps registry chain filters without silently coercing unsupported chains', () => {
    assert.equal(chainIdForRegistryFilter(), 1);
    assert.equal(chainIdForRegistryFilter('mainnet'), 1);
    assert.equal(chainIdForRegistryFilter('base'), 8453);
    assert.equal(chainIdForRegistryFilter('base-sepolia'), 84532);
    assert.equal(chainIdForRegistryFilter('aztec'), undefined);
  });

  it('labels chain and testnet context per card', () => {
    assert.equal(registryChainLabel(1), 'Ethereum');
    assert.equal(registryChainLabel(999999), 'EIP-155 999999');

    const badges = registryChainBadges(BASE_AGENT);
    assert.deepEqual(
      badges.map(b => b.label),
      ['Base Sepolia', 'testnet']
    );
  });

  it('classifies health status badges', () => {
    assert.deepEqual(registryHealthBadge('healthy'), {
      key: 'health',
      label: 'health: healthy',
      variant: 'green',
    });
    assert.deepEqual(registryHealthBadge('degraded'), {
      key: 'health',
      label: 'health: degraded',
      variant: 'yellow',
    });
    assert.deepEqual(registryHealthBadge('offline'), {
      key: 'health',
      label: 'health: offline',
      variant: 'red',
    });
    assert.equal(registryHealthBadge(null), null);
  });

  it('surfaces categories, tags, and tools with dedupe and overflow', () => {
    const badges = registryCapabilityBadges(
      {
        ...BASE_AGENT,
        tags: ['quant', 'Quant', 'risk'],
        services: {
          mcp: { tools: ['portfolio', 'portfolio', 'risk'] },
        },
      },
      4
    );

    assert.deepEqual(
      badges.map(b => b.label),
      [
        'category: finance',
        'tag: quant',
        'tag: risk',
        'tool: portfolio',
        '+1 more',
      ]
    );
  });
});
