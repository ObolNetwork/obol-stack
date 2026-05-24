import fs from 'node:fs';

import { KubeConfig } from '@kubernetes/client-node';

/**
 * Shared KubeConfig for all in-cluster service clients.
 *
 * Constructing a new KubeConfig per request (which every Route Handler
 * was doing) discards the underlying https.Agent connection pool —
 * each call paid full TLS handshake + TCP setup against the apiserver.
 * Cluster-internal traffic doesn't have to be fast, but it doesn't
 * have to be wasteful either.
 *
 * Module-level singleton: initialized lazily on first use, reused for
 * the lifetime of the Node process.
 *
 * In-cluster (KUBERNETES_SERVICE_HOST set): loads from the mounted
 * service account. Otherwise loads from the file path in
 * NEXT_PUBLIC_KUBECONFIG (matching the per-client behavior this
 * replaces — we intentionally don't fall back to loadFromDefault() so
 * local dev keeps failing loudly when the env var is missing).
 */
let _kc: KubeConfig | undefined;

export function sharedKubeConfig(): KubeConfig {
  if (_kc) return _kc;
  const kc = new KubeConfig();
  if (process.env.KUBERNETES_SERVICE_HOST) {
    kc.loadFromCluster();
  } else {
    const kubeconfigPath = process.env.NEXT_PUBLIC_KUBECONFIG;
    if (!kubeconfigPath) {
      throw new Error(
        'NEXT_PUBLIC_KUBECONFIG environment variable is required for local development. ' +
          'Set it to the path of your kubeconfig file.'
      );
    }
    if (!fs.existsSync(kubeconfigPath)) {
      throw new Error(`Kubeconfig file not found at: ${kubeconfigPath}`);
    }
    kc.loadFromFile(kubeconfigPath);
  }
  _kc = kc;
  return _kc;
}

/**
 * Test-only: reset the cached singleton so tests can exercise the
 * branching logic with different env states. Not exported via the
 * package barrel; consumers must reach into this module directly.
 */
export function _resetSharedKubeConfigForTests(): void {
  _kc = undefined;
}
