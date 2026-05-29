import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  agentRegistrationExplorerUrl,
  agentRegistrationLabel,
  parseAgentRegistry,
} from './agent-registration.ts';

describe('parseAgentRegistry', () => {
  it('parses eip155 CAIP-10 registry records', () => {
    assert.deepEqual(
      parseAgentRegistry(
        'eip155:84532:0x8004A818BFB912233c491871b3d84c89A494BD9e'
      ),
      {
        caip2Network: 'eip155:84532',
        chainId: 84532,
        chainLabel: 'Base Sepolia',
        registryAddress: '0x8004A818BFB912233c491871b3d84c89A494BD9e',
        explorerBaseUrl: 'https://sepolia.basescan.org',
      }
    );
  });

  it('labels known mainnet chains', () => {
    assert.equal(
      parseAgentRegistry('eip155:1:0x1111111111111111111111111111111111111111')
        ?.chainLabel,
      'Ethereum Mainnet'
    );
    assert.equal(
      parseAgentRegistry(
        'eip155:8453:0x1111111111111111111111111111111111111111'
      )?.chainLabel,
      'Base'
    );
  });

  it('returns null for malformed registry strings', () => {
    assert.equal(parseAgentRegistry('eip155:84532:not-an-address'), null);
    assert.equal(parseAgentRegistry('cosmos:1:address'), null);
  });
});

describe('agentRegistrationLabel', () => {
  it('uses the known chain label when possible', () => {
    assert.equal(
      agentRegistrationLabel({
        agentId: 777,
        agentRegistry:
          'eip155:84532:0x8004A818BFB912233c491871b3d84c89A494BD9e',
      }),
      'Agent #777 on Base Sepolia'
    );
  });

  it('falls back to the raw registry when parsing fails', () => {
    assert.equal(
      agentRegistrationLabel({
        agentId: '42',
        agentRegistry: 'malformed-registry',
      }),
      'Agent #42 on malformed-registry'
    );
  });
});

describe('agentRegistrationExplorerUrl', () => {
  it('builds explorer nft links for known chains', () => {
    assert.equal(
      agentRegistrationExplorerUrl({
        agentId: 777,
        agentRegistry:
          'eip155:84532:0x8004A818BFB912233c491871b3d84c89A494BD9e',
      }),
      'https://sepolia.basescan.org/nft/0x8004A818BFB912233c491871b3d84c89A494BD9e/777'
    );
  });

  it('returns null for unknown chains', () => {
    assert.equal(
      agentRegistrationExplorerUrl({
        agentId: 42,
        agentRegistry:
          'eip155:999999:0x1111111111111111111111111111111111111111',
      }),
      null
    );
  });
});
