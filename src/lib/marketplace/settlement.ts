/**
 * Settlement helpers for the My Purchases drawer.
 *
 * Bridges the buy-side PurchaseRequest CR (which only knows its endpoint URL
 * + chain) to the verifier-side `obol_x402_verifier_last_payment_success_seconds`
 * gauge (labeled by offer namespace, offer name, chain).
 *
 * The cluster's Traefik route pattern for x402-gated services is
 * `/services/<offer-name>/...`, so parsing the path of the endpoint URL gives
 * us the offer name. The offer namespace is whichever namespace holds the
 * ServiceOffer CR (resolved server-side from the offers list).
 */

/**
 * Extract the offer name embedded in a PurchaseRequest endpoint URL.
 *
 * Examples
 *   http://obol.stack:8080/services/ollama-gated/api/chat -> "ollama-gated"
 *   http://traefik.traefik.svc.cluster.local/services/rs-7b/v1 -> "rs-7b"
 *   https://demo.obol.tech/services/legal-summarizer -> "legal-summarizer"
 *   https://api.example.com/v1/chat -> null  (external x402 seller)
 *   "" -> null
 */
export function offerNameFromEndpoint(endpoint: string): string | null {
  if (!endpoint) return null;
  let pathname: string;
  try {
    pathname = new URL(endpoint).pathname;
  } catch {
    // Fall back to substring match for non-URL strings.
    pathname = endpoint;
  }
  const match = pathname.match(/\/services\/([^/?#]+)/);
  if (!match) return null;
  const name = match[1];
  return name.length > 0 ? name : null;
}

/**
 * Render a settlement delta in human-readable form.
 *
 *   formatAge(0)      -> "0s ago"
 *   formatAge(12)     -> "12s ago"
 *   formatAge(180)    -> "3m ago"
 *   formatAge(7200)   -> "2h ago"
 *   formatAge(172800) -> "2d ago"
 *   formatAge(-5)     -> "0s ago" (clock skew between FE and Prom)
 */
export function formatAge(deltaSec: number): string {
  if (!Number.isFinite(deltaSec) || deltaSec < 0) return '0s ago';
  const s = Math.floor(deltaSec);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

/**
 * Format a Unix-seconds timestamp as a UTC RFC3339 string (no sub-second
 * precision, ending in `Z`). Returns `'—'` when the input is missing or
 * non-finite — the empty-state callers want to render.
 */
export function formatSettlementTimestamp(
  unixSec: number | null | undefined
): string {
  if (unixSec === null || unixSec === undefined) return '—';
  if (!Number.isFinite(unixSec) || unixSec <= 0) return '—';
  return new Date(unixSec * 1000).toISOString().replace(/\.\d+Z$/, 'Z');
}
