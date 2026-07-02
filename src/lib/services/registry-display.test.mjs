import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  chainIdForRegistryFilter,
  rankRegistryAgents,
  registryAgentQualityScore,
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

  it('ranks registry agents by quality signals, descending', () => {
    // Rich profile: real name, long description, protocol, x402, two
    // endpoints, tools, verified, healthy, 8004scan score + stars.
    const rich = {
      ...BASE_AGENT,
      token_id: '1',
      name: 'Portfolio Analyst',
      description:
        'Full-featured research agent with a genuinely useful description.',
    };
    // Mid profile: real name + description and one endpoint, no other signals.
    const mid = {
      ...BASE_AGENT,
      token_id: '2',
      name: 'Mid Agent',
      description: 'A real description over twenty characters long.',
      x402_supported: false,
      supported_protocols: [],
      services: { web: { endpoint: 'https://mid.example.test' } },
      total_score: 0,
      star_count: 0,
      is_verified: false,
      health_status: null,
    };
    // Bare profiles: trivial names (address / "Agent #n"), no metadata.
    const bareAddress = {
      ...mid,
      token_id: '3',
      name: '0xdeadbeef',
      description: '',
      services: null,
    };
    const bareFiller = {
      ...bareAddress,
      token_id: '4',
      name: 'Agent #4',
    };

    assert.ok(
      registryAgentQualityScore(rich) > registryAgentQualityScore(mid)
    );
    assert.ok(
      registryAgentQualityScore(mid) > registryAgentQualityScore(bareAddress)
    );
    assert.equal(registryAgentQualityScore(bareAddress), 0);
    assert.equal(registryAgentQualityScore(bareFiller), 0);

    const ranked = rankRegistryAgents([bareAddress, mid, bareFiller, rich]);
    assert.deepEqual(
      ranked.map(a => a.token_id),
      // Stable sort: the two zero-score agents keep API order (3 before 4).
      ['1', '2', '3', '4']
    );
  });

  it('rank is stable for ties and does not mutate the input array', () => {
    const tieA = {
      ...BASE_AGENT,
      token_id: '10',
      name: 'Agent #10',
      description: '',
      services: null,
      x402_supported: false,
      supported_protocols: [],
      total_score: 0,
      star_count: 0,
      is_verified: false,
      health_status: null,
    };
    const tieB = { ...tieA, token_id: '11', name: 'Agent #11' };

    const input = [tieB, tieA];
    const ranked = rankRegistryAgents(input);

    assert.notEqual(ranked, input);
    assert.deepEqual(
      input.map(a => a.token_id),
      ['11', '10']
    );
    assert.deepEqual(
      ranked.map(a => a.token_id),
      ['11', '10']
    );
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
