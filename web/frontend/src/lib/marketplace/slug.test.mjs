import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { decodeSlugParam, isWellFormedSlug } from './slug.ts';

describe('isWellFormedSlug', () => {
  it('accepts cluster and ERC-8004 slugs', () => {
    assert.equal(isWellFormedSlug('llm/obol-demo'), true);
    assert.equal(isWellFormedSlug('agent-quant/quant'), true);
    assert.equal(isWellFormedSlug('erc8004/8453/42'), true);
  });

  it('rejects anything that cannot resolve', () => {
    for (const bad of [
      '',
      'nope',
      'a/b/c',
      'LLM/demo',
      '../etc/passwd',
      'llm/',
      'erc8004/base/1',
    ]) {
      assert.equal(isWellFormedSlug(bad), false, bad);
    }
  });
});

describe('decodeSlugParam', () => {
  it('decodes an encoded slash once', () => {
    assert.equal(decodeSlugParam('llm%2Fobol-demo'), 'llm/obol-demo');
    assert.equal(decodeSlugParam('llm/obol-demo'), 'llm/obol-demo');
  });

  it('returns null for malformed encodings', () => {
    assert.equal(decodeSlugParam('%E0%A4%A'), null);
  });
});
