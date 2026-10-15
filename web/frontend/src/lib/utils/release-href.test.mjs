import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

describe('release-href', async () => {
  const { isReleaseTag, releaseHref } = await import('./release-href.ts');

  it('links real release tags to the tag page', () => {
    assert.equal(
      releaseHref('obol-stack', 'v0.1.28-rc3'),
      'https://github.com/ObolNetwork/obol-stack/releases/tag/v0.1.28-rc3'
    );
  });

  it('normalises a goreleaser version with no leading v', () => {
    // goreleaser stamps internal/version.Version as {{ .Version }} — 0.14.0.
    assert.equal(
      releaseHref('obol-stack', '0.14.0'),
      'https://github.com/ObolNetwork/obol-stack/releases/tag/v0.14.0'
    );
  });

  it('falls back to the releases index for non-release labels', () => {
    // 'local'  — local docker build with no VERSION build-arg
    // 'dev'    — obol CLI built without the goreleaser ldflags
    // 'unknown'— obol-stack-config carried no obolVersion
    for (const label of ['local', 'dev', 'unknown', '']) {
      assert.equal(
        releaseHref('obol-stack', label),
        'https://github.com/ObolNetwork/obol-stack/releases',
        `expected releases index for ${JSON.stringify(label)}`
      );
    }
  });

  it('falls back for git-describe stamps from non-tag CI builds', () => {
    // the old front-end repo's CI used `git describe --tags --always --dirty` for
    // every push that is not a tag, so :latest and :next carry this shape.
    assert.equal(
      releaseHref('obol-stack', 'v0.1.28-rc3-14-gabc1234'),
      'https://github.com/ObolNetwork/obol-stack/releases'
    );
    assert.equal(isReleaseTag('v0.1.28-rc3-14-gabc1234'), false);
    assert.equal(isReleaseTag('v0.1.28-rc3'), true);
  });
});
