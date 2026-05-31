export const LOCAL_STOREFRONT_URL = 'http://obol.stack:8080';
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

export function publicResourceUrl(
  tunnelURL: string | null | undefined,
  path: string
): string {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  return `${publicStorefrontUrl(tunnelURL)}${normalizedPath}`;
}
