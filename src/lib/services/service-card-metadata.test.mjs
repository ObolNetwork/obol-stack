import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { describe, it } from 'node:test';

describe('ServiceCard metadata presentation', () => {
  it('renders registration domains alongside skills and trust metadata', async () => {
    const source = await readFile(
      'src/components/molecules/ServiceCard/ServiceCard.tsx',
      'utf8'
    );

    assert.match(source, /offer\.registration\?\.domains \?\? \[\]/);
    assert.match(source, /Domain: \{domain\}/);
  });

  it('keeps service metadata text above the 10px legibility floor', async () => {
    const source = await readFile(
      'src/components/molecules/ServiceCard/ServiceCard.tsx',
      'utf8'
    );

    assert.doesNotMatch(source, /fontSize: '10px'/);
    assert.doesNotMatch(source, /opacity: 0\.5/);
    assert.match(source, /missing: \{ color: '\$muted', opacity: 1 \}/);
  });
});
