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
  it('builds the legacy OpenClaw dashboard deeplink', () => {
    assert.equal(
      getAgentDashboardUrl('default', 'openclaw'),
      'http://openclaw-default.obol.stack'
    );
    assert.equal(
      getAgentDashboardUrl('obol-agent', 'openclaw'),
      'http://openclaw-obol-agent.obol.stack'
    );
  });

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

  it('keeps Hermes and OpenClaw namespace parsing distinct', () => {
    assert.equal(getRuntimeFromNamespace('hermes-obol-agent'), 'hermes');
    assert.equal(getRuntimeFromNamespace('openclaw-obol-agent'), 'openclaw');
    assert.equal(getRuntimeFromNamespace('obol-agent'), null);

    assert.equal(
      getAgentIdFromNamespace('hermes-obol-agent', 'hermes'),
      'obol-agent'
    );
    assert.equal(
      getAgentIdFromNamespace('openclaw-research-agent', 'openclaw'),
      'research-agent'
    );
  });

  it('builds in-cluster service URLs for both runtimes', () => {
    assert.equal(
      getAgentServiceUrl({
        id: 'obol-agent',
        runtime: 'hermes',
        isInCluster: true,
        openclawLocalUrl: 'http://localhost:18789',
      }),
      'http://hermes.hermes-obol-agent.svc.cluster.local:8642'
    );

    assert.equal(
      getAgentServiceUrl({
        id: 'default',
        runtime: 'openclaw',
        isInCluster: true,
        openclawLocalUrl: 'http://localhost:18789',
      }),
      'http://openclaw.openclaw-default.svc.cluster.local:18789'
    );
  });

  it('keeps browser deeplinks separate from local development service URLs', () => {
    assert.equal(
      getAgentServiceUrl({
        id: 'obol-agent',
        runtime: 'hermes',
        isInCluster: false,
        openclawLocalUrl: 'http://localhost:18789',
      }),
      'http://localhost:8642'
    );

    assert.equal(
      getAgentServiceUrl({
        id: 'obol-agent',
        runtime: 'hermes',
        isInCluster: false,
        openclawLocalUrl: 'http://localhost:18789',
        hermesLocalUrl: 'http://localhost:9000',
      }),
      'http://localhost:9000'
    );

    assert.equal(
      getAgentServiceUrl({
        id: 'obol-agent',
        runtime: 'openclaw',
        isInCluster: false,
        openclawLocalUrl: 'http://localhost:18789',
      }),
      'http://localhost:18789'
    );
  });

  it('keeps runtime config aligned with generated namespaces', () => {
    assert.equal(
      getAgentNamespace('obol-agent', 'hermes'),
      `${AGENT_RUNTIME_CONFIG.hermes.namespacePrefix}obol-agent`
    );
    assert.equal(
      getAgentNamespace('obol-agent', 'openclaw'),
      `${AGENT_RUNTIME_CONFIG.openclaw.namespacePrefix}obol-agent`
    );
  });

  it('marks only default runtime agents as sell-capable', () => {
    assert.equal(
      isDefaultSellAgent({ runtime: 'hermes', id: 'obol-agent' }),
      true
    );
    assert.equal(
      isDefaultSellAgent({ runtime: 'openclaw', id: 'default' }),
      true
    );
    assert.equal(
      isDefaultSellAgent({ runtime: 'hermes', id: 'research-agent' }),
      false
    );
    assert.equal(
      isDefaultSellAgent({ runtime: 'openclaw', id: 'obol-agent' }),
      false
    );
  });
});
