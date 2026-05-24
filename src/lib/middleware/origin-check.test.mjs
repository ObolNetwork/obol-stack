import assert from 'node:assert/strict';
import { afterEach, describe, it } from 'node:test';

import { rejectCrossOrigin } from './origin-check.ts';

function makeRequest(method, headers = {}) {
  // NextRequest accepts the standard Request initializer; for the helper's
  // purposes (method + headers only) a plain Request is sufficient because
  // rejectCrossOrigin only touches request.method and request.headers.get.
  return new Request('http://obol.stack:8080/api/sell/create', {
    method,
    headers,
  });
}

describe('rejectCrossOrigin', () => {
  afterEach(() => {
    delete process.env.OBOL_FRONTEND_ALLOWED_ORIGINS;
  });

  it('passes GET requests without checking Origin', () => {
    const res = rejectCrossOrigin(makeRequest('GET'));
    assert.equal(res, null);
  });

  it('passes HEAD requests without checking Origin', () => {
    const res = rejectCrossOrigin(makeRequest('HEAD'));
    assert.equal(res, null);
  });

  it('passes OPTIONS preflight without checking Origin', () => {
    const res = rejectCrossOrigin(makeRequest('OPTIONS'));
    assert.equal(res, null);
  });

  it('passes POST with allowed Origin (obol.stack:8080)', () => {
    const res = rejectCrossOrigin(
      makeRequest('POST', { origin: 'http://obol.stack:8080' })
    );
    assert.equal(res, null);
  });

  it('passes POST with allowed Origin (obol.stack:8443 https)', () => {
    const res = rejectCrossOrigin(
      makeRequest('POST', { origin: 'https://obol.stack:8443' })
    );
    assert.equal(res, null);
  });

  it('passes POST with default-port Origin http://obol.stack (port-omitted = 80)', () => {
    // Real browser requests to http://obol.stack/ send Origin: http://obol.stack
    // (no port — default port 80 is omitted per RFC 6454). Production Traefik
    // binds port 80; without this case the prod browser flow gets 403.
    const res = rejectCrossOrigin(
      makeRequest('POST', { origin: 'http://obol.stack' })
    );
    assert.equal(res, null);
  });

  it('passes POST with default-port Origin https://obol.stack (port-omitted = 443)', () => {
    const res = rejectCrossOrigin(
      makeRequest('POST', { origin: 'https://obol.stack' })
    );
    assert.equal(res, null);
  });

  it('blocks POST from evil.com Origin with 403', async () => {
    const res = rejectCrossOrigin(
      makeRequest('POST', { origin: 'http://evil.com' })
    );
    assert.notEqual(res, null);
    assert.equal(res.status, 403);
    const body = await res.json();
    assert.match(body.error, /Origin http:\/\/evil\.com not allowed/);
  });

  it('blocks POST with neither Origin nor Referer with 403', async () => {
    const res = rejectCrossOrigin(makeRequest('POST'));
    assert.notEqual(res, null);
    assert.equal(res.status, 403);
    const body = await res.json();
    assert.match(body.error, /missing Origin header/);
  });

  it('passes POST with no Origin but allowed Referer prefix', () => {
    const res = rejectCrossOrigin(
      makeRequest('POST', {
        referer: 'http://obol.stack:8080/sell/new',
      })
    );
    assert.equal(res, null);
  });

  it('blocks POST with no Origin and disallowed Referer with 403', async () => {
    const res = rejectCrossOrigin(
      makeRequest('POST', { referer: 'http://evil.com/attack' })
    );
    assert.notEqual(res, null);
    assert.equal(res.status, 403);
  });

  it('honors OBOL_FRONTEND_ALLOWED_ORIGINS override', () => {
    process.env.OBOL_FRONTEND_ALLOWED_ORIGINS = 'http://localhost:3000';
    const res = rejectCrossOrigin(
      makeRequest('POST', { origin: 'http://localhost:3000' })
    );
    assert.equal(res, null);
  });

  it('rejects default obol.stack origin when override is set', async () => {
    process.env.OBOL_FRONTEND_ALLOWED_ORIGINS = 'http://localhost:3000';
    const res = rejectCrossOrigin(
      makeRequest('POST', { origin: 'http://obol.stack:8080' })
    );
    assert.notEqual(res, null);
    assert.equal(res.status, 403);
  });

  it('supports comma-separated override list', () => {
    process.env.OBOL_FRONTEND_ALLOWED_ORIGINS =
      'http://localhost:3000, http://localhost:4000';
    const res = rejectCrossOrigin(
      makeRequest('POST', { origin: 'http://localhost:4000' })
    );
    assert.equal(res, null);
  });
});
