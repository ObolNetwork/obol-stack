import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { k8sErrorStatus } from './k8s-errors.ts';

describe('k8sErrorStatus', () => {
  it('reads ApiException.code', () => {
    assert.equal(k8sErrorStatus({ code: 409 }), 409);
  });
  it('reads legacy statusCode shapes', () => {
    assert.equal(k8sErrorStatus({ statusCode: 404 }), 404);
    assert.equal(k8sErrorStatus({ response: { statusCode: 403 } }), 403);
  });
  it('ignores non-numeric codes and junk', () => {
    assert.equal(k8sErrorStatus({ code: 'ECONNREFUSED' }), undefined);
    assert.equal(k8sErrorStatus(null), undefined);
    assert.equal(k8sErrorStatus('boom'), undefined);
  });
});
