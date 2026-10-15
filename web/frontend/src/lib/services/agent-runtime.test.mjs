import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  AGENT_RUNTIME_CONFIG,
  getAgentDashboardHostname,
  getAgentDashboardUrl,
  getAgentIdFromNamespace,
  getAgentNamespace,
  getAgentServiceUrl,
  getRuntimeFromNamespace,
  isDefaultSellAgent,
} from './agent-runtime.ts';

describe('agent runtime URL helpers', () => {
  it('builds Hermes dashboard deeplinks for the default obol-agent and named agents', () => {
    const cases = [
      {
        id: 'obol-agent',
        expected: 'http://obol-agent.obol.stack',
      },
      {
        id: 'research-agent',
        expected: 'http://hermes-research-agent-ui.obol.stack',
      },
      {
        id: 'validator-ops',
        expected: 'http://hermes-validator-ops-ui.obol.stack',
      },
    ];

    for (const testCase of cases) {
      assert.equal(
        getAgentDashboardUrl(testCase.id, 'hermes'),
        testCase.expected
      );
    }
  });

  it('builds Hermes dashboard hostnames for UI cards', () => {
    assert.equal(
      getAgentDashboardHostname('obol-agent', 'hermes'),
      'obol-agent.obol.stack'
    );
    assert.equal(
      getAgentDashboardHostname('research-agent', 'hermes'),
      'hermes-research-agent-ui.obol.stack'
    );
  });

  it('parses Hermes namespaces and rejects unrelated ones', () => {
    assert.equal(getRuntimeFromNamespace('hermes-obol-agent'), 'hermes');
    assert.equal(getRuntimeFromNamespace('obol-agent'), null);
    assert.equal(getRuntimeFromNamespace('llm'), null);

    assert.equal(
      getAgentIdFromNamespace('hermes-obol-agent', 'hermes'),
      'obol-agent'
    );
  });

  it('builds in-cluster service URLs', () => {
    assert.equal(
      getAgentServiceUrl({
        id: 'obol-agent',
        runtime: 'hermes',
        isInCluster: true,
      }),
      'http://hermes.hermes-obol-agent.svc.cluster.local:8642'
    );
  });

  it('keeps browser deeplinks separate from local development service URLs', () => {
    assert.equal(
      getAgentServiceUrl({
        id: 'obol-agent',
        runtime: 'hermes',
        isInCluster: false,
      }),
      'http://localhost:8642'
    );

    assert.equal(
      getAgentServiceUrl({
        id: 'obol-agent',
        runtime: 'hermes',
        isInCluster: false,
        hermesLocalUrl: 'http://localhost:9000',
      }),
      'http://localhost:9000'
    );
  });

  it('keeps runtime config aligned with generated namespaces', () => {
    assert.equal(
      getAgentNamespace('obol-agent', 'hermes'),
      `${AGENT_RUNTIME_CONFIG.hermes.namespacePrefix}obol-agent`
    );
  });

  it('marks only the default agent as sell-capable', () => {
    assert.equal(
      isDefaultSellAgent({ runtime: 'hermes', id: 'obol-agent' }),
      true
    );
    assert.equal(
      isDefaultSellAgent({ runtime: 'hermes', id: 'research-agent' }),
      false
    );
    assert.equal(
      isDefaultSellAgent({ runtime: 'hermes', id: 'default' }),
      false
    );
  });
});
