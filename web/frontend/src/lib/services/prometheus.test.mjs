import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, it } from 'node:test';

import { PrometheusClient } from './prometheus.ts';

const ORIGINAL_FETCH = globalThis.fetch;

function jsonResponse(body, { ok = true, status = 200 } = {}) {
  return {
    ok,
    status,
    statusText: ok ? 'OK' : 'Error',
    headers: new Headers({ 'content-type': 'application/json' }),
    async json() {
      return body;
    },
  };
}

describe('PrometheusClient.lastPaymentSuccessByOfferChain', () => {
  beforeEach(() => {
    // Each test installs its own stub.
  });

  afterEach(() => {
    globalThis.fetch = ORIGINAL_FETCH;
  });

  it('maps the gauge result into "<ns>/<name>/<chain>" -> unix seconds', async () => {
    let capturedUrl;
    globalThis.fetch = async (url) => {
      capturedUrl = String(url);
      return jsonResponse({
        status: 'success',
        data: {
          resultType: 'vector',
          result: [
            {
              metric: {
                offer_namespace: 'llm',
                offer_name: 'ollama-gated',
                chain: 'base-sepolia',
                asset_symbol: 'USDC',
              },
              value: [1779999300, '1779999262'],
            },
            {
              metric: {
                offer_namespace: 'default',
                offer_name: 'rs-7b',
                chain: 'base',
              },
              value: [1779999300, '1779999000.5'],
            },
          ],
        },
      });
    };

    const client = new PrometheusClient('http://prom.example.com');
    const out = await client.lastPaymentSuccessByOfferChain();

    assert.ok(capturedUrl.includes('obol_x402_verifier_last_payment_success_seconds'));
    assert.equal(out.size, 2);
    assert.equal(out.get('llm/ollama-gated/base-sepolia'), 1779999262);
    assert.equal(out.get('default/rs-7b/base'), 1779999000.5);
  });

  it('keeps the most recent timestamp when multiple verifier replicas report', async () => {
    globalThis.fetch = async () =>
      jsonResponse({
        status: 'success',
        data: {
          resultType: 'vector',
          result: [
            {
              metric: {
                offer_namespace: 'llm',
                offer_name: 'foo',
                chain: 'base',
              },
              value: [1779999300, '100'],
            },
            {
              metric: {
                offer_namespace: 'llm',
                offer_name: 'foo',
                chain: 'base',
              },
              value: [1779999300, '200'],
            },
          ],
        },
      });

    const client = new PrometheusClient('http://prom.example.com');
    const out = await client.lastPaymentSuccessByOfferChain();
    assert.equal(out.get('llm/foo/base'), 200);
  });

  it('skips rows missing any of the three labels', async () => {
    globalThis.fetch = async () =>
      jsonResponse({
        status: 'success',
        data: {
          resultType: 'vector',
          result: [
            { metric: { offer_name: 'foo', chain: 'base' }, value: [0, '1'] },
            {
              metric: { offer_namespace: 'llm', chain: 'base' },
              value: [0, '2'],
            },
            {
              metric: { offer_namespace: 'llm', offer_name: 'foo' },
              value: [0, '3'],
            },
            {
              metric: {
                offer_namespace: 'llm',
                offer_name: 'foo',
                chain: 'base',
              },
              value: [0, '4'],
            },
          ],
        },
      });

    const client = new PrometheusClient('http://prom.example.com');
    const out = await client.lastPaymentSuccessByOfferChain();
    assert.equal(out.size, 1);
    assert.equal(out.get('llm/foo/base'), 4);
  });

  it('returns an empty Map when Prometheus is unavailable', async () => {
    globalThis.fetch = async () => {
      throw new Error('ECONNREFUSED');
    };
    const client = new PrometheusClient('http://prom.example.com');
    const out = await client.lastPaymentSuccessByOfferChain();
    assert.equal(out.size, 0);
  });

  it('returns an empty Map when Prometheus replies with a non-OK status', async () => {
    globalThis.fetch = async () =>
      jsonResponse({}, { ok: false, status: 503 });
    const client = new PrometheusClient('http://prom.example.com');
    const out = await client.lastPaymentSuccessByOfferChain();
    assert.equal(out.size, 0);
  });

  it('returns an empty Map when the result is empty', async () => {
    globalThis.fetch = async () =>
      jsonResponse({
        status: 'success',
        data: { resultType: 'vector', result: [] },
      });
    const client = new PrometheusClient('http://prom.example.com');
    const out = await client.lastPaymentSuccessByOfferChain();
    assert.equal(out.size, 0);
  });
});
