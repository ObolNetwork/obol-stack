/**
 * GitHub release links for the footer version row.
 *
 * Only released builds carry a real tag. Non-tag CI builds are stamped with
 * `git describe` output (v0.1.28-rc3-14-gabc1234) by CI,
 * local `just dev-frontend` builds get no VERSION build-arg at all ('local'),
 * and an obol CLI built without the goreleaser ldflags reports 'dev'. Linking
 * any of those to /releases/tag/<label> produces a 404, so those fall back to
 * the repository's releases index.
 */

// The front-end ships from obol-stack (web/frontend) since v0.17.
export type ObolRepo = 'obol-stack';

const RELEASE_TAG = /^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/;
const GIT_DESCRIBE_SUFFIX = /-\d+-g[0-9a-f]{7,}$/;

export function isReleaseTag(tag: string): boolean {
  const trimmed = tag.trim();
  return RELEASE_TAG.test(trimmed) && !GIT_DESCRIBE_SUFFIX.test(trimmed);
}

export function releaseHref(repo: ObolRepo, tag: string): string {
  const releases = `https://github.com/ObolNetwork/${repo}/releases`;
  const trimmed = tag.trim();

  if (!isReleaseTag(trimmed)) {
    return releases;
  }

  const normalized = trimmed.startsWith('v') ? trimmed : `v${trimmed}`;
  return `${releases}/tag/${normalized}`;
}
