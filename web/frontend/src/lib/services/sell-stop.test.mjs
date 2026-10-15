import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  buildDrainPatch,
  goDurationToMs,
  isValidGoDuration,
  parseStopRequest,
} from '../../app/api/sell/stop/drain.ts';

describe('isValidGoDuration', () => {
  it('accepts common Go duration shapes', () => {
    for (const v of ['1h', '30m', '5m', '90s', '1h30m', '500ms', '2h15m30s']) {
      assert.equal(isValidGoDuration(v), true, `expected ${v} to be valid`);
    }
  });

  it('rejects empty, malformed, or hostile strings', () => {
    for (const v of [
      '',
      '1',
      '1d',
      'abc',
      '1h ',
      ' 1h',
      '1h;rm -rf',
      '1'.repeat(40),
    ]) {
      assert.equal(isValidGoDuration(v), false, `expected ${v} to be invalid`);
    }
  });
});

describe('parseStopRequest', () => {
  it('defaults grace to 1h and force to false', () => {
    const r = parseStopRequest({ name: 'demo', namespace: 'llm' });
    assert.equal(r.ok, true);
    if (r.ok) {
      assert.equal(r.value.grace, '1h');
      assert.equal(r.value.force, false);
    }
  });

  it('honours an explicit grace string', () => {
    const r = parseStopRequest({
      name: 'demo',
      namespace: 'llm',
      grace: '30m',
    });
    assert.equal(r.ok, true);
    if (r.ok) assert.equal(r.value.grace, '30m');
  });

  it('force=true overrides grace to 0s', () => {
    const r = parseStopRequest({
      name: 'demo',
      namespace: 'llm',
      grace: '1h',
      force: true,
    });
    assert.equal(r.ok, true);
    if (r.ok) {
      assert.equal(r.value.grace, '0s');
      assert.equal(r.value.force, true);
    }
  });

  it('rejects missing or non-RFC1123 name', () => {
    for (const name of [undefined, '', 'BadName', '../etc', 'a'.repeat(0)]) {
      const r = parseStopRequest({ name, namespace: 'llm' });
      assert.equal(r.ok, false);
    }
  });

  it('rejects missing or non-RFC1123 namespace', () => {
    const r = parseStopRequest({ name: 'demo', namespace: '' });
    assert.equal(r.ok, false);
  });

  it('rejects unparseable grace', () => {
    const r = parseStopRequest({
      name: 'demo',
      namespace: 'llm',
      grace: '1 hour',
    });
    assert.equal(r.ok, false);
  });
});

describe('goDurationToMs', () => {
  it('converts simple durations', () => {
    assert.equal(goDurationToMs('1h'), 3_600_000);
    assert.equal(goDurationToMs('30m'), 1_800_000);
    assert.equal(goDurationToMs('5s'), 5_000);
    assert.equal(goDurationToMs('500ms'), 500);
  });

  it('sums concatenated unit terms', () => {
    assert.equal(goDurationToMs('1h30m'), 5_400_000);
    assert.equal(goDurationToMs('2h15m30s'), 8_130_000);
  });

  it('returns 0 for the force-stop sentinel', () => {
    assert.equal(goDurationToMs('0s'), 0);
  });
});

describe('buildDrainPatch', () => {
  it('emits spec.drainAt + spec.drainGracePeriod with computed end time', () => {
    const now = new Date('2026-01-01T00:00:00.000Z');
    const { patch, drainEndsAt } = buildDrainPatch(
      { name: 'demo', namespace: 'llm', grace: '1h', force: false },
      now
    );
    assert.equal(patch.spec.drainAt, '2026-01-01T00:00:00.000Z');
    assert.equal(patch.spec.drainGracePeriod, '1h');
    assert.equal(drainEndsAt.toISOString(), '2026-01-01T01:00:00.000Z');
  });

  it('force-stop emits 0s and drainEndsAt = now', () => {
    const now = new Date('2026-01-01T00:00:00.000Z');
    const { patch, drainEndsAt } = buildDrainPatch(
      { name: 'demo', namespace: 'llm', grace: '0s', force: true },
      now
    );
    assert.equal(patch.spec.drainGracePeriod, '0s');
    assert.equal(drainEndsAt.toISOString(), '2026-01-01T00:00:00.000Z');
  });
});
