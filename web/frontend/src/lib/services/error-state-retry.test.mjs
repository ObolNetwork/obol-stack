import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const RETRY_SURFACES = [
  'src/components/organisms/DashboardContent/DashboardContent.tsx',
  'src/components/organisms/ServiceList/ServiceList.tsx',
  'src/components/organisms/AgentRegistry/AgentRegistry.tsx',
];

describe('ErrorState retry wiring', () => {
  it('passes retry callbacks to retryable list error states', async () => {
    for (const file of RETRY_SURFACES) {
      const source = await readFile(resolve(process.cwd(), file), 'utf8');
      assert.match(
        source,
        /(refetch|onRefresh)/,
        `${file} should read a retry callback`
      );
      assert.match(
        source,
        /<ErrorState[\s\S]*onRetry=\{(refetch|onRefresh)\}/,
        `${file} should pass retry callback to ErrorState`
      );
    }
  });
});
