import assert from 'node:assert/strict';
import { describe, it, beforeEach, afterEach } from 'node:test';

describe('cluster-url', async () => {
  const saved = process.env.KUBERNETES_SERVICE_HOST;

  beforeEach(() => {
    delete process.env.KUBERNETES_SERVICE_HOST;
  });

  afterEach(() => {
    if (saved === undefined) {
      delete process.env.KUBERNETES_SERVICE_HOST;
    } else {
      process.env.KUBERNETES_SERVICE_HOST = saved;
    }
  });

  const {
    isClusterInternalUrl,
    localizeObolStackPort,
    resolveMetricsFetchUrl,
    isUrlReachableFromRuntime,
  } = await import('./cluster-url.ts');

  it('detects cluster-internal hostnames', () => {
    assert.equal(
      isClusterInternalUrl(
        'http://ethereum-execution.ethereum-foo.svc.cluster.local:8545'
      ),
      true
    );
    assert.equal(isClusterInternalUrl('http://obol.stack:8080/rpc'), false);
  });

  it('adds :8080 for bare obol.stack URLs', () => {
    assert.equal(
      localizeObolStackPort('http://obol.stack/rpc/ethereum'),
      'http://obol.stack:8080/rpc/ethereum'
    );
  });

  it('skips internal URLs in local dev', () => {
    assert.equal(
      resolveMetricsFetchUrl(
        'http://ethereum-execution.ethereum-foo.svc.cluster.local:8545'
      ),
      null
    );
    assert.equal(isUrlReachableFromRuntime('http://foo.svc.cluster.local:1'), false);
  });
});
