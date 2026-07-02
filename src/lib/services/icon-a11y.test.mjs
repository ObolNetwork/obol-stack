import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

async function read(path) {
  return readFile(resolve(process.cwd(), path), 'utf8');
}

describe('icon-only control accessibility', () => {
  it('labels copy icon buttons with the same text as their title', async () => {
    const source = await read('src/components/atoms/CopyButton/CopyButton.tsx');
    assert.match(source, /type="button"/);
    assert.match(source, /aria-label=\{title\}/);
  });

  it('uses semantic close buttons in chat sidebars', async () => {
    for (const file of [
      'src/components/molecules/AgentChatSidebar/AgentChatSidebar.tsx',
      'src/components/molecules/OpenClawChatSidebar/OpenClawChatSidebar.tsx',
    ]) {
      const source = await read(file);
      // Close controls are real <button>s via the canonical Button atom.
      assert.match(source, /from '@\/components\/atoms\/Button'/);
      assert.match(source, /aria-label="Close chat"/);
    }
  });

  it('labels modal icon buttons', async () => {
    const walletModal = await read(
      'src/components/molecules/WalletModal/WalletModal.tsx'
    );
    assert.match(walletModal, /aria-label="Close wallet modal"/);
  });

  it('labels icon-only service and explorer links', async () => {
    const serviceCard = await read(
      'src/components/molecules/ServiceCard/ServiceCard.tsx'
    );
    assert.match(serviceCard, /aria-label="Open service in new tab"/);

    const walletBalances = await read(
      'src/components/molecules/WalletBalanceList/WalletBalanceList.tsx'
    );
    assert.match(walletBalances, /aria-label=\{`View \$\{b\.token\.symbol\}/);
  });
});
