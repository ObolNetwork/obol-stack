import assert from 'node:assert/strict';
import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { describe, it } from 'node:test';

async function collectTsxFiles(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = [];

  for (const entry of entries) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await collectTsxFiles(path)));
    } else if (entry.isFile() && entry.name.endsWith('.tsx')) {
      files.push(path);
    }
  }

  return files;
}

describe('external links', () => {
  it('uses noopener and noreferrer on every target blank link', async () => {
    const files = [
      ...(await collectTsxFiles('src/components')),
      ...(await collectTsxFiles('src/app')),
    ];
    const missing = [];
    const targetBlankTag = /<[^>]+target="_blank"[^>]*>/gs;

    for (const file of files) {
      const source = await readFile(file, 'utf8');
      const tags = source.matchAll(targetBlankTag);

      for (const match of tags) {
        if (!/rel="noopener noreferrer"/.test(match[0])) {
          missing.push(`${file}: ${match[0].replace(/\s+/g, ' ')}`);
        }
      }
    }

    assert.deepEqual(missing, []);
  });
});
