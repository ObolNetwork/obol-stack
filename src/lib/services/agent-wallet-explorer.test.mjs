import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const AGENT_CARD =
  'src/components/molecules/AgentInstanceCard/AgentInstanceCard.tsx';

describe('agent wallet explorer links', () => {
  it('does not render a chain-ambiguous mainnet Etherscan wallet shortcut', async () => {
    const source = await readFile(resolve(process.cwd(), AGENT_CARD), 'utf8');

    assert.equal(
      source.includes('https://etherscan.io/address/${primaryAddress}'),
      false,
      'wallet header must not hardcode primaryAddress to mainnet Etherscan'
    );
    assert.match(
      source,
      /explorerUrlFor\(b\.chain, b\.token, primaryAddress\)/,
      'per-chain balance rows should keep using chain-specific explorer URLs'
    );
  });
});
