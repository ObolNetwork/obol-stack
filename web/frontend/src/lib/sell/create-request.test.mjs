import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { parseCreateSellRequest } from './create-request.ts';

const WALLET = '0x1111111111111111111111111111111111111111';

const http = {
  name: 'my-api',
  namespace: 'llm',
  type: 'http',
  wallet: WALLET,
  chain: 'base',
  token: 'USDC',
  pricePerRequest: '0.001',
  upstream: 'ollama',
  port: 11434,
  healthPath: '/api/tags',
};

const inference = {
  name: 'obol-demo',
  namespace: 'llm',
  type: 'inference',
  wallet: WALLET,
  chain: 'base-sepolia',
  token: 'OBOL',
  pricePerRequest: '1',
  model: 'qwen3.5:4b',
};

const agent = {
  name: 'quant',
  namespace: 'agent-quant',
  type: 'agent',
  wallet: WALLET,
  chain: 'base',
  token: 'USDC',
  pricePerRequest: '0.05',
  agentRef: { name: 'quant', namespace: 'agent-quant' },
  skills: ['research', 'trading signals'],
  description: 'Market alpha.\nPer turn.',
};

function expectError(body, re) {
  const res = parseCreateSellRequest(body);
  assert.equal(res.ok, false, `expected rejection for ${JSON.stringify(body)}`);
  assert.match(res.error, re);
}

describe('parseCreateSellRequest', () => {
  it('accepts the bodies the UI sends', () => {
    for (const body of [http, inference, agent]) {
      const res = parseCreateSellRequest(body);
      assert.equal(res.ok, true, JSON.stringify(res));
    }
  });

  it('rejects non-object bodies', () => {
    expectError(null, /JSON object/);
    expectError([], /JSON object/);
    expectError('x', /JSON object/);
  });

  it('reports missing required fields', () => {
    expectError(
      { name: 'a' },
      /Missing required fields: wallet, chain, pricePerRequest/
    );
  });

  it('rejects unknown fields', () => {
    expectError({ ...http, extra: 1 }, /Unknown field\(s\): extra/);
  });

  it('validates the name and upstream as DNS-1123 labels', () => {
    expectError({ ...http, name: 'My_API' }, /Invalid name/);
    expectError({ ...http, name: 'a'.repeat(64) }, /Invalid name/);
    expectError({ ...http, upstream: 'svc.other-ns' }, /Invalid upstream/);
    expectError({ ...http, upstream: '-bad' }, /Invalid upstream/);
    expectError({ ...http, upstream: 'http://evil' }, /Invalid upstream/);
  });

  it('validates the port range and type', () => {
    expectError({ ...http, port: 0 }, /Invalid port/);
    expectError({ ...http, port: 65536 }, /Invalid port/);
    expectError({ ...http, port: 80.5 }, /Invalid port/);
    expectError({ ...http, port: '80' }, /Invalid port/);
    assert.equal(parseCreateSellRequest({ ...http, port: 65535 }).ok, true);
  });

  it('validates healthPath', () => {
    expectError({ ...http, healthPath: 'health' }, /Invalid healthPath/);
    expectError({ ...http, healthPath: '/he alth' }, /Invalid healthPath/);
    expectError({ ...http, healthPath: '/x\n' }, /Invalid healthPath/);
    assert.equal(
      parseCreateSellRequest({ ...http, healthPath: '/health/readiness?x=1' })
        .ok,
      false
    );
    assert.equal(parseCreateSellRequest({ ...http, healthPath: '/' }).ok, true);
  });

  it('validates the wallet address', () => {
    expectError({ ...http, wallet: '0x123' }, /Invalid wallet/);
    expectError(
      { ...http, wallet: WALLET.replace('0x', '') },
      /Invalid wallet/
    );
    expectError({ ...http, wallet: `${WALLET}0` }, /Invalid wallet/);
    expectError(
      { ...http, wallet: '0xZZ11111111111111111111111111111111111111' },
      /Invalid wallet/
    );
  });

  it('requires a positive decimal string price', () => {
    for (const bad of ['0', '0.000', '-1', '1e3', '.5', '01', '1.', 'abc']) {
      expectError({ ...http, pricePerRequest: bad }, /Invalid pricePerRequest/);
    }
    expectError({ ...http, pricePerRequest: 0.5 }, /Invalid pricePerRequest/);
    expectError(
      { ...http, pricePerRequest: '' },
      /Missing required fields: pricePerRequest/
    );
    for (const good of ['0.001', '1', '12.5', '0.000000000000000001']) {
      assert.equal(
        parseCreateSellRequest({ ...http, pricePerRequest: good }).ok,
        true,
        good
      );
    }
  });

  it('validates enums and optional nested fields', () => {
    expectError({ ...http, type: 'ssh' }, /Invalid type/);
    expectError({ ...http, token: 'DAI' }, /Invalid token/);
    expectError({ ...inference, model: '' }, /Invalid model/);
    expectError({ ...inference, model: 'm; rm -rf /' }, /Invalid model/);
    expectError(
      { ...agent, agentRef: { name: 'Bad Name' } },
      /Invalid agentRef/
    );
    expectError(
      { ...agent, agentRef: { name: 'ok', extra: 'x' } },
      /Invalid agentRef/
    );
    expectError({ ...agent, skills: 'research' }, /Invalid skills/);
    expectError({ ...agent, skills: [''] }, /Invalid skills/);
    expectError(
      { ...agent, description: 'x'.repeat(2001) },
      /Invalid description/
    );
  });
});
