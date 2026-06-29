import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  buildAgentCreateWorkflowCli,
  buildAgentNewCli,
  buildSellAgentCli,
  buildSellInferenceCli,
  buildSellStatusCli,
} from './cli-commands.ts';

describe('sell cli-commands', () => {
  it('builds sell inference with positional name', () => {
    const cmd = buildSellInferenceCli({
      name: 'rc13-inference',
      model: 'qwen3.5:4b',
      pricePerRequest: '0.001',
      token: 'OBOL',
      chain: 'ethereum',
      payTo: '0xabc',
    });
    assert.match(cmd, /^obol sell inference rc13-inference/);
    assert.match(cmd, /--model qwen3\.5:4b/);
    assert.match(cmd, /--pay-to 0xabc/);
  });

  it('builds sell agent for sub-agent name', () => {
    const cmd = buildSellAgentCli({
      agentName: 'rc13-test',
      name: 'rc13-test',
      pricePerRequest: '0.01',
      token: 'USDC',
      chain: 'base-sepolia',
      payTo: '0xdef',
    });
    assert.match(cmd, /^obol sell agent rc13-test/);
  });

  it('builds minimal agent create + sell workflow', () => {
    const workflow = buildAgentCreateWorkflowCli('obol-agent');
    assert.equal(workflow.steps.length, 2);
    assert.equal(workflow.steps[0].command, 'obol agent new obol-agent');
    assert.equal(workflow.steps[1].command, 'obol sell agent obol-agent');
    assert.match(workflow.copyText, /obol agent new obol-agent/);
  });

  it('builds agent new with wallet', () => {
    const cmd = buildAgentNewCli({
      name: 'quant',
      model: 'claude-sonnet-4-6',
      skills: 'addresses,gas',
    });
    assert.match(cmd, /^obol agent new quant/);
    assert.match(cmd, /--create-wallet/);
  });

  it('builds sell status', () => {
    assert.equal(
      buildSellStatusCli('foo', 'agent-foo'),
      'obol sell status foo -n agent-foo'
    );
  });
});
