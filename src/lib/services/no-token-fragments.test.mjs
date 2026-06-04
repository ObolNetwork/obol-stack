import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const DASHBOARD_LINK_FILES = [
  'src/components/molecules/AgentInstanceCard/AgentInstanceCard.tsx',
  'src/components/molecules/OpenClawInstanceCard/OpenClawInstanceCard.tsx',
  'src/hooks/useAgents.ts',
  'src/hooks/useOpenClaw.ts',
  'src/app/api/openclaw/token/route.ts',
];

describe('dashboard token URLs', () => {
  it('does not put bearer tokens into dashboard URL fragments', async () => {
    const forbiddenFragment = ['#', 'token'].join('');
    for (const file of DASHBOARD_LINK_FILES) {
      const source = await readFile(resolve(process.cwd(), file), 'utf8');
      assert.equal(
        source.includes(forbiddenFragment),
        false,
        `${file} must not contain dashboard token fragments`
      );
    }
  });

  it('does not fetch token routes from dashboard card hooks', async () => {
    for (const file of ['src/hooks/useAgents.ts', 'src/hooks/useOpenClaw.ts']) {
      const source = await readFile(resolve(process.cwd(), file), 'utf8');
      assert.equal(
        /\/api\/(?:agents|openclaw)\/token/.test(source),
        false,
        `${file} must not fetch dashboard tokens for links`
      );
    }
  });
});
