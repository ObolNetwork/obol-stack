import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const RETRY_SURFACES = [
  'src/components/organisms/AgentList/AgentList.tsx',
  'src/components/organisms/OpenClawList/OpenClawList.tsx',
  'src/components/organisms/ServiceList/ServiceList.tsx',
  'src/components/organisms/AgentRegistry/AgentRegistry.tsx',
];

describe('ErrorState retry wiring', () => {
  it('passes hook refetch callbacks to retryable list error states', async () => {
    for (const file of RETRY_SURFACES) {
      const source = await readFile(resolve(process.cwd(), file), 'utf8');
      assert.match(source, /refetch/, `${file} should read a refetch callback`);
      assert.match(
        source,
        /<ErrorState[\s\S]*onRetry=\{refetch\}/,
        `${file} should pass refetch to ErrorState`
      );
    }
  });
});
