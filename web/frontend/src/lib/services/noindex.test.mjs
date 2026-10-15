import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

describe('operator console noindex metadata', () => {
  it('exports noindex/nofollow robots metadata from the root layout', async () => {
    const source = await readFile(
      resolve(process.cwd(), 'src/app/layout.tsx'),
      'utf8'
    );

    assert.match(source, /robots:\s*{/);
    assert.match(source, /index:\s*false/);
    assert.match(source, /follow:\s*false/);
  });

  it('serves a robots.txt route that disallows every path', async () => {
    const source = await readFile(
      resolve(process.cwd(), 'src/app/robots.ts'),
      'utf8'
    );

    assert.match(source, /userAgent:\s*['"]\*['"]/);
    assert.match(source, /disallow:\s*['"]\/['"]/);
  });
});
