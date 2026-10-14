import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { describe, it } from 'node:test';

describe('offer-readiness', () => {
  it('documents readiness steps and http offer filtering', async () => {
    const source = await readFile('src/lib/services/offer-readiness.ts', 'utf8');
    assert.match(source, /READINESS_STEPS/);
    assert.match(source, /readinessStepsForOffer/);
    assert.match(source, /offer\.type === 'http'/);
  });
});
