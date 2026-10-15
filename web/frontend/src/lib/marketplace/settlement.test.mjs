import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  formatAge,
  formatSettlementTimestamp,
  offerNameFromEndpoint,
} from './settlement.ts';

describe('offerNameFromEndpoint', () => {
  it('extracts the offer name from a host-routed Traefik URL', () => {
    assert.equal(
      offerNameFromEndpoint('http://obol.stack:8080/services/ollama-gated/api/chat'),
      'ollama-gated'
    );
  });

  it('extracts the offer name from the in-cluster Traefik URL', () => {
    assert.equal(
      offerNameFromEndpoint(
        'http://traefik.traefik.svc.cluster.local/services/rs-7b/v1'
      ),
      'rs-7b'
    );
  });

  it('extracts the offer name from a public tunnel URL', () => {
    assert.equal(
      offerNameFromEndpoint('https://demo.obol.tech/services/legal-summarizer'),
      'legal-summarizer'
    );
  });

  it('returns null for external x402 sellers without /services/<name>', () => {
    assert.equal(
      offerNameFromEndpoint('https://api.example.com/v1/chat'),
      null
    );
  });

  it('returns null for an empty endpoint', () => {
    assert.equal(offerNameFromEndpoint(''), null);
  });

  it('falls back to substring matching for non-URL strings', () => {
    // PurchaseRequest CRs allow free-form endpoint strings; not all are
    // parseable as URLs.
    assert.equal(
      offerNameFromEndpoint('/services/qwen36-deep'),
      'qwen36-deep'
    );
  });

  it('ignores trailing slashes and query strings', () => {
    assert.equal(
      offerNameFromEndpoint('http://obol.stack:8080/services/foo/'),
      'foo'
    );
    assert.equal(
      offerNameFromEndpoint('http://obol.stack:8080/services/foo?x=1'),
      'foo'
    );
  });
});

describe('formatAge', () => {
  it('formats zero as 0s ago', () => {
    assert.equal(formatAge(0), '0s ago');
  });

  it('formats seconds', () => {
    assert.equal(formatAge(12), '12s ago');
    assert.equal(formatAge(59), '59s ago');
  });

  it('formats minutes', () => {
    assert.equal(formatAge(60), '1m ago');
    assert.equal(formatAge(180), '3m ago');
    assert.equal(formatAge(3599), '59m ago');
  });

  it('formats hours', () => {
    assert.equal(formatAge(3600), '1h ago');
    assert.equal(formatAge(7200), '2h ago');
    assert.equal(formatAge(86_399), '23h ago');
  });

  it('formats days', () => {
    assert.equal(formatAge(86_400), '1d ago');
    assert.equal(formatAge(172_800), '2d ago');
  });

  it('clamps negative deltas to 0s ago', () => {
    // Clock skew between FE wall-clock and Prom scrape can produce small
    // negative deltas — never render those as "-3s ago".
    assert.equal(formatAge(-5), '0s ago');
  });

  it('handles non-finite deltas defensively', () => {
    assert.equal(formatAge(Number.NaN), '0s ago');
    assert.equal(formatAge(Number.POSITIVE_INFINITY), '0s ago');
  });
});

describe('formatSettlementTimestamp', () => {
  it('formats a Unix-seconds value as RFC3339 with second precision', () => {
    // 2026-05-24T20:14:22Z = 1779653662
    assert.equal(
      formatSettlementTimestamp(1779653662),
      '2026-05-24T20:14:22Z'
    );
  });

  it('renders an em-dash for null/undefined/non-positive inputs', () => {
    assert.equal(formatSettlementTimestamp(null), '—');
    assert.equal(formatSettlementTimestamp(undefined), '—');
    assert.equal(formatSettlementTimestamp(0), '—');
    assert.equal(formatSettlementTimestamp(-1), '—');
  });

  it('renders an em-dash for non-finite inputs', () => {
    assert.equal(formatSettlementTimestamp(Number.NaN), '—');
    assert.equal(formatSettlementTimestamp(Number.POSITIVE_INFINITY), '—');
  });
});
