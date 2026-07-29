export const LOCAL_STOREFRONT_URL = 'http://obol.stack:8080';
export const STOREFRONT_PREVIEW_HOST = 'storefront-preview.obol.stack';
export const AGENT_REGISTRATION_PATH = '/.well-known/agent-registration.json';

export function normalizePublicBaseUrl(
  url: string | null | undefined
): string | null {
  const trimmed = url?.trim();
  if (!trimmed) return null;
  return trimmed.replace(/\/+$/, '');
}

export function publicStorefrontUrl(
  tunnelURL: string | null | undefined
): string {
  return normalizePublicBaseUrl(tunnelURL) ?? LOCAL_STOREFRONT_URL;
}

/**
 * Local-only operator preview origin.
 *
 * Reuses the operator dashboard's scheme/port so Traefik load-balancer port
 * drift (e.g. :57425 instead of :8080) still frames the same ingress.
 * Never points at the public tunnel hostname.
 */
export function localStorefrontPreviewUrl(
  operatorOrigin?: string | null
): string {
  const fallback = LOCAL_STOREFRONT_URL;
  const raw =
    operatorOrigin?.trim() ||
    (typeof window !== 'undefined' ? window.location.origin : fallback);
  try {
    const url = new URL(raw || fallback);
    url.hostname = STOREFRONT_PREVIEW_HOST;
    url.pathname = '/';
    url.search = '';
    url.hash = '';
    return url.toString().replace(/\/$/, '');
  } catch {
    return `http://${STOREFRONT_PREVIEW_HOST}:8080`;
  }
}

export function publicResourceUrl(
  tunnelURL: string | null | undefined,
  path: string
): string {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  return `${publicStorefrontUrl(tunnelURL)}${normalizedPath}`;
}

export function publicServiceEndpoint(
  endpoint: string | undefined,
  tunnelURL: string | null | undefined
): string | null {
  const storefront = publicStorefrontUrl(tunnelURL);

  try {
    const base = new URL(storefront);
    const basePath = base.pathname.replace(/\/$/, '');
    if (!endpoint) return `${base.origin}${basePath}`;

    const relativeBase = new URL(`${base.origin}${basePath || ''}/`);
    const resolvedEndpoint = new URL(endpoint, relativeBase);
    return `${base.origin}${basePath}${resolvedEndpoint.pathname}${resolvedEndpoint.search}${resolvedEndpoint.hash}`;
  } catch {
    return null;
  }
}
