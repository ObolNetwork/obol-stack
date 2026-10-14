import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const AGENT_CARD =
  'src/components/molecules/AgentInstanceCard/AgentInstanceCard.tsx';
const WALLET_BALANCE_LIST =
  'src/components/molecules/WalletBalanceList/WalletBalanceList.tsx';

describe('agent wallet explorer links', () => {
  it('does not render a chain-ambiguous mainnet Etherscan wallet shortcut', async () => {
    const agentCard = await readFile(resolve(process.cwd(), AGENT_CARD), 'utf8');
    const walletList = await readFile(
      resolve(process.cwd(), WALLET_BALANCE_LIST),
      'utf8'
    );

    assert.equal(
      agentCard.includes('https://etherscan.io/address/${primaryAddress}'),
      false,
      'wallet header must not hardcode primaryAddress to mainnet Etherscan'
    );
    assert.match(
      walletList,
      /explorerUrlFor\(b\.chain, b\.token, walletAddress\)/,
      'per-chain balance rows should keep using chain-specific explorer URLs'
    );
  });
});
