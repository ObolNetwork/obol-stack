/**
 * Health probe paths for `obol sell http` upstreams (ServiceOffer.spec.upstream.healthPath).
 * Must match what the controller calls when reconciling UpstreamHealthy.
 */
export function resolveUpstreamHealthPath(upstreamService: string): string {
  const svc = upstreamService.trim().toLowerCase();
  if (svc === 'ollama') return '/api/tags';
  if (svc === 'litellm') return '/health/readiness';
  return '/health/readiness';
}
