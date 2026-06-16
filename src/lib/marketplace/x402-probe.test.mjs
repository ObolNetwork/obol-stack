import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  buildServiceProbeUrl,
  parseProbeResponse,
  probeModelForOffer,
} from './x402-probe.ts';

describe('x402 probe helpers', () => {
  it('builds chat completions URL for inference', () => {
    assert.equal(
      buildServiceProbeUrl('https://foo.trycloudflare.com', {
        name: 'rc13',
        type: 'inference',
      }),
      'https://foo.trycloudflare.com/services/rc13/v1/chat/completions'
    );
  });

  it('builds root URL for http offers', () => {
    assert.equal(
      buildServiceProbeUrl('http://obol.stack:8080', {
        name: 'demo',
        type: 'http',
      }),
      'http://obol.stack:8080/services/demo/'
    );
  });

  it('parses 402 as gated', () => {
    const r = parseProbeResponse(
      402,
      JSON.stringify({
        accepts: [
          {
            maxAmountRequired: '1000',
            network: 'eip155:84532',
            asset: 'USDC',
          },
        ],
      })
    );
    assert.equal(r.gated, true);
    assert.equal(r.acceptsCount, 1);
    assert.match(r.summary, /402/);
  });

  it('flags 200 as not gated', () => {
    const r = parseProbeResponse(200, '{}');
    assert.equal(r.gated, false);
  });

  it('resolves model from agent resolution', () => {
    assert.equal(
      probeModelForOffer({
        name: 'quant',
        agentResolution: { model: 'hermes-agent' },
      }),
      'hermes-agent'
    );
  });
});
