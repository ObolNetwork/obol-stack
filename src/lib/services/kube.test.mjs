import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, it } from 'node:test';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import {
  _resetSharedKubeConfigForTests,
  sharedKubeConfig,
} from './kube.ts';

// Minimal valid kubeconfig — enough for KubeConfig.loadFromFile() to parse and
// for getCurrentContext() to resolve, which is all the singleton path needs.
const FAKE_KUBECONFIG_YAML = `apiVersion: v1
kind: Config
current-context: test
clusters:
  - name: test-cluster
    cluster:
      server: https://example.invalid:6443
      insecure-skip-tls-verify: true
contexts:
  - name: test
    context:
      cluster: test-cluster
      user: test-user
users:
  - name: test-user
    user:
      token: dummy-token
`;

describe('sharedKubeConfig', () => {
  const savedEnv = {
    KUBERNETES_SERVICE_HOST: process.env.KUBERNETES_SERVICE_HOST,
    NEXT_PUBLIC_KUBECONFIG: process.env.NEXT_PUBLIC_KUBECONFIG,
  };
  let kubeconfigPath;

  beforeEach(() => {
    _resetSharedKubeConfigForTests();
    delete process.env.KUBERNETES_SERVICE_HOST;
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'kube-test-'));
    kubeconfigPath = path.join(dir, 'kubeconfig.yaml');
    fs.writeFileSync(kubeconfigPath, FAKE_KUBECONFIG_YAML, 'utf8');
    process.env.NEXT_PUBLIC_KUBECONFIG = kubeconfigPath;
  });

  afterEach(() => {
    _resetSharedKubeConfigForTests();
    if (kubeconfigPath && fs.existsSync(kubeconfigPath)) {
      fs.rmSync(path.dirname(kubeconfigPath), { recursive: true, force: true });
    }
    if (savedEnv.KUBERNETES_SERVICE_HOST === undefined) {
      delete process.env.KUBERNETES_SERVICE_HOST;
    } else {
      process.env.KUBERNETES_SERVICE_HOST = savedEnv.KUBERNETES_SERVICE_HOST;
    }
    if (savedEnv.NEXT_PUBLIC_KUBECONFIG === undefined) {
      delete process.env.NEXT_PUBLIC_KUBECONFIG;
    } else {
      process.env.NEXT_PUBLIC_KUBECONFIG = savedEnv.NEXT_PUBLIC_KUBECONFIG;
    }
  });

  it('returns the same instance across successive calls', () => {
    const a = sharedKubeConfig();
    const b = sharedKubeConfig();
    assert.strictEqual(a, b, 'expected identical singleton across calls');
  });

  it('loads from the path in NEXT_PUBLIC_KUBECONFIG when not in cluster', () => {
    const kc = sharedKubeConfig();
    assert.equal(kc.getCurrentContext(), 'test');
  });

  it('throws when NEXT_PUBLIC_KUBECONFIG is unset and not in cluster', () => {
    delete process.env.NEXT_PUBLIC_KUBECONFIG;
    _resetSharedKubeConfigForTests();
    assert.throws(() => sharedKubeConfig(), /NEXT_PUBLIC_KUBECONFIG/);
  });

  it('throws when the kubeconfig path does not exist', () => {
    process.env.NEXT_PUBLIC_KUBECONFIG = '/nonexistent/path/kubeconfig.yaml';
    _resetSharedKubeConfigForTests();
    assert.throws(() => sharedKubeConfig(), /not found/);
  });

  it('takes the in-cluster branch when KUBERNETES_SERVICE_HOST is set', () => {
    // Set up a bogus kubeconfig path. The local-dev branch would throw
    // "Kubeconfig file not found"; the in-cluster branch ignores that env
    // var entirely. So either:
    //   - loadFromCluster() succeeds (whatever the in-cluster shim does on
    //     macOS — the client-node SDK no-ops with empty cluster state) and
    //     we get a KubeConfig back without ever consulting the bogus path,
    //   - or loadFromCluster() throws — but the error must NOT be our
    //     "file not found" message, proving we took the in-cluster branch.
    process.env.KUBERNETES_SERVICE_HOST = '10.0.0.1';
    process.env.NEXT_PUBLIC_KUBECONFIG = '/definitely/does/not/exist';
    _resetSharedKubeConfigForTests();

    let kc;
    let caught;
    try {
      kc = sharedKubeConfig();
    } catch (err) {
      caught = err;
    }

    if (caught) {
      assert.ok(
        !/kubeconfig file not found/i.test(String(caught.message)),
        `expected in-cluster branch (no file check), but saw file-not-found error: ${caught.message}`
      );
    } else {
      assert.ok(kc, 'sharedKubeConfig() returned no value in cluster branch');
    }
  });
});
