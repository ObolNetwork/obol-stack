export function isInKubernetesCluster(): boolean {
  return Boolean(process.env.KUBERNETES_SERVICE_HOST);
}

export function isClusterInternalUrl(url: string): boolean {
  try {
    const host = new URL(url).hostname;
    return host.endsWith('.svc.cluster.local') || host.endsWith('.svc');
  } catch {
    return false;
  }
}

/** Map bare `obol.stack` hostnames to the Mac-accessible Traefik port. */
export function localizeObolStackPort(url: string): string {
  try {
    const parsed = new URL(url);
    if (
      (parsed.hostname === 'obol.stack' ||
        parsed.hostname.endsWith('.obol.stack')) &&
      !parsed.port
    ) {
      parsed.port = '8080';
      return parsed.toString();
    }
  } catch {
    // keep original url
  }
  return url;
}

/**
 * Pick an RPC URL the Next.js server can reach.
 * In-cluster: use internal service DNS. Local dev: skip `.svc.cluster.local`.
 */
export function resolveMetricsFetchUrl(
  internal: string,
  external?: string
): string | null {
  if (isInKubernetesCluster()) {
    return internal;
  }
  if (external) {
    return localizeObolStackPort(external);
  }
  if (isClusterInternalUrl(internal)) {
    return null;
  }
  return localizeObolStackPort(internal);
}

export function isUrlReachableFromRuntime(url: string): boolean {
  if (isInKubernetesCluster()) {
    return true;
  }
  return !isClusterInternalUrl(url);
}

/**
 * eRPC base URL for browser-side wallet balance reads.
 * Local Next on :3000 must hit Traefik at :8080/rpc, not relative /rpc.
 */
export function clientErpcBaseUrl(): string {
  const configured = process.env.NEXT_PUBLIC_ERPC_URL?.trim();
  if (configured) return configured.replace(/\/$/, '');

  if (typeof window !== 'undefined') {
    const { hostname, port, protocol } = window.location;
    if (hostname === 'obol.stack' || hostname.endsWith('.obol.stack')) {
      const traefikPort = !port || port === '3000' ? '8080' : port;
      return `${protocol}//${hostname}:${traefikPort}/rpc`;
    }
  }

  return '/rpc';
}
