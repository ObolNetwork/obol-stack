import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  StackUnreachableError,
  classifyFetchFailure,
  isStackUnreachable,
  STACK_UNREACHABLE,
} from './errors.ts';

describe('StackUnreachableError', () => {
  it('carries the STACK_UNREACHABLE code and a status', () => {
    const err = new StackUnreachableError('boom', 503);
    assert.equal(err.code, STACK_UNREACHABLE);
    assert.equal(err.status, 503);
    assert.equal(err.name, 'StackUnreachableError');
    assert.ok(err instanceof Error);
  });

  it('preserves the underlying cause', () => {
    const cause = new Error('connect ECONNREFUSED');
    const err = new StackUnreachableError('boom', 0, { cause });
    assert.equal(err.cause, cause);
  });
});

describe('isStackUnreachable', () => {
  it('returns true for the typed error and false otherwise', () => {
    assert.equal(isStackUnreachable(new StackUnreachableError('x')), true);
    assert.equal(isStackUnreachable(new Error('x')), false);
    assert.equal(isStackUnreachable('string'), false);
    assert.equal(isStackUnreachable(null), false);
    assert.equal(isStackUnreachable(undefined), false);
  });
});

describe('classifyFetchFailure', () => {
  it('wraps network failures (status 0) as unreachable', () => {
    const wrapped = classifyFetchFailure(new TypeError('failed to fetch'), 0);
    assert.ok(isStackUnreachable(wrapped));
    assert.equal(wrapped.message, 'Obol Stack unreachable');
  });

  it('wraps 5xx responses as unreachable with status included', () => {
    const wrapped = classifyFetchFailure(new Error('HTTP 503'), 503);
    assert.ok(isStackUnreachable(wrapped));
    assert.match(wrapped.message, /Obol Stack unreachable \(HTTP 503\)/);
    assert.equal(wrapped.status, 503);
  });

  it('does NOT wrap 4xx responses', () => {
    const e = new Error('HTTP 404');
    const wrapped = classifyFetchFailure(e, 404);
    assert.equal(isStackUnreachable(wrapped), false);
    assert.equal(wrapped, e);
  });

  it('stringifies non-Error throws when not unreachable', () => {
    const wrapped = classifyFetchFailure('weird', 400);
    assert.equal(isStackUnreachable(wrapped), false);
    assert.equal(wrapped.message, 'weird');
  });
});
