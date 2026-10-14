/**
 * HTTP status of a failed Kubernetes API call, regardless of client shape:
 * `ApiException.code` (@kubernetes/client-node 1.x/2.x generated APIs),
 * or `statusCode` / `response.statusCode` (legacy request-based clients).
 */
export function k8sErrorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const e = error as {
    code?: unknown;
    statusCode?: unknown;
    response?: { statusCode?: unknown };
  };
  for (const v of [e.code, e.statusCode, e.response?.statusCode]) {
    if (typeof v === 'number') return v;
  }
  return undefined;
}
