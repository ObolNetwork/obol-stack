import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  buyPyCommand,
  computePreflight,
  kubectlBuyCommand,
  litellmCurlExample,
  multiplyAtomicString,
  multiplyDecimalString,
} from './buy-command.ts';

const baseItem = {
  name: 'demo-quant',
  endpoint: 'https://demo.obol.tech/services/demo-quant',
  modelOrRuntime: 'hermes · qwen3.5:9b',
  remoteModel: undefined,
  price: '10',
  priceAtomicUnits: '10000000000000000000',
};

describe('multiplyDecimalString', () => {
  it('preserves fractional digit count', () => {
    assert.equal(multiplyDecimalString('0.001', 5), '0.005');
    assert.equal(multiplyDecimalString('0.001', 1000), '1.000');
    assert.equal(multiplyDecimalString('2.50', 4), '10.00');
  });
  it('handles integer-only values', () => {
    assert.equal(multiplyDecimalString('10', 3), '30');
    assert.equal(multiplyDecimalString('0', 100), '0');
  });
  it('handles count = 0', () => {
    assert.equal(multiplyDecimalString('0.001', 0), '0');
  });
});

describe('multiplyAtomicString', () => {
  it('multiplies BigInt-safely', () => {
    assert.equal(
      multiplyAtomicString('10000000000000000000', 100),
      '1000000000000000000000'
    );
  });
  it('returns 0 for missing value', () => {
    assert.equal(multiplyAtomicString('', 5), '0');
  });
});

describe('computePreflight', () => {
  it('rounds count to a positive int and computes totals', () => {
    const pre = computePreflight(baseItem, 3.7);
    assert.equal(pre.count, 3);
    assert.equal(pre.totalHuman, '30');
    assert.equal(pre.totalAtomic, '30000000000000000000');
  });
  it('clamps non-positive count to 1', () => {
    const pre = computePreflight(baseItem, 0);
    assert.equal(pre.count, 1);
    assert.equal(pre.totalHuman, '10');
  });
});

describe('buyPyCommand', () => {
  it('emits a single-quoted, line-continuated buy command', () => {
    const cmd = buyPyCommand(baseItem, 10);
    assert.match(cmd, /^buy\.py buy \\\n /);
    assert.match(cmd, /'demo-quant'/);
    assert.match(
      cmd,
      /--endpoint 'https:\/\/demo\.obol\.tech\/services\/demo-quant'/
    );
    assert.match(cmd, /--model 'qwen3\.5:9b'/);
    assert.match(cmd, /--count 10/);
  });
  it("uses agent runtime's model side when 'runtime · model' is given", () => {
    const cmd = buyPyCommand(baseItem, 1);
    assert.match(cmd, /--model 'qwen3\.5:9b'/);
    assert.doesNotMatch(cmd, /hermes/);
  });
  it('honors explicit remoteModel override', () => {
    const cmd = buyPyCommand({ ...baseItem, remoteModel: 'llama-2-7b' }, 1);
    assert.match(cmd, /--model 'llama-2-7b'/);
  });
  it('escapes single quotes inside name/endpoint', () => {
    const cmd = buyPyCommand(
      { ...baseItem, name: "can't-buy", endpoint: "http://x'y" },
      1
    );
    assert.match(cmd, /'can'\\''t-buy'/);
    assert.match(cmd, /'http:\/\/x'\\''y'/);
  });
});

describe('kubectlBuyCommand', () => {
  it('wraps buy.py with kubectl exec on the litellm pod', () => {
    const cmd = kubectlBuyCommand(baseItem, 25);
    assert.match(
      cmd,
      /^kubectl -n llm exec -it deploy\/litellm -c x402-buyer --/
    );
    assert.match(cmd, /buy\.py buy/);
    assert.match(cmd, /--count 25/);
  });
});

describe('litellmCurlExample', () => {
  it('targets paid/<model> with a content-type header', () => {
    const cmd = litellmCurlExample(baseItem);
    assert.match(cmd, /paid\/qwen3\.5:9b/);
    assert.match(cmd, /content-type: application\/json/);
    assert.match(cmd, /\/v1\/chat\/completions/);
  });
});
