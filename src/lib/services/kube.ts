import fs from 'node:fs';
import path from 'node:path';

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
 * service account — no kubeconfig file needed.
 *
 * Local / host dev (no KUBERNETES_SERVICE_HOST): resolveKubeconfigPath()
 * picks the first existing path in order:
 *   1. KUBECONFIG
 *   2. OBOL_CONFIG_DIR/kubeconfig.yaml
 *
 * This module does NOT read NEXT_PUBLIC_KUBECONFIG or ~/.config/obol/.
 * Those fallbacks exist only in bash port-forward scripts (see
 * scripts/port-forward-erpc.sh) for `pnpm dev` kubectl helpers — not
 * used by Next.js API routes.
 */
let _kc: KubeConfig | undefined;

export function resolveKubeconfigPath(): string | undefined {
  // Server-side chain only — see module doc for bash script differences.
  const candidates = [
    process.env.KUBECONFIG,
    process.env.OBOL_CONFIG_DIR
      ? path.join(process.env.OBOL_CONFIG_DIR, 'kubeconfig.yaml')
      : undefined,
  ].filter((p): p is string => Boolean(p && p.trim()));

  return candidates[0];
}

export function sharedKubeConfig(): KubeConfig {
  if (_kc) return _kc;
  const kc = new KubeConfig();
  if (process.env.KUBERNETES_SERVICE_HOST) {
    kc.loadFromCluster();
  } else {
    const kubeconfigPath = resolveKubeconfigPath();
    if (!kubeconfigPath) {
      throw new Error(
        'KUBECONFIG environment variable is required for local development. ' +
          'Set it to your obol kubeconfig path (e.g. ~/.config/obol/kubeconfig.yaml).'
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
