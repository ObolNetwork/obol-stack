import type { NextRequest, NextResponse } from 'next/server';

/**
 * Allowed origins for mutating requests. The frontend is exposed only via
 * the Traefik HTTPRoute hostname-restricted to "obol.stack", which is NEVER
 * exposed via the Cloudflared tunnel (see CLAUDE.md "Security: Tunnel
 * Exposure"). The only legitimate Origin header value comes from an
 * obol.stack URL resolved via local DNS = trusted same machine.
 *
 * Without this check, a malicious page on the same machine (another browser
 * tab pointed at evil.com) can submit a form POST cross-origin to
 * /api/sell/create — the browser blocks reading the response (SOP) but
 * does NOT block the side effect of creating a ServiceOffer.
 *
 * The hostname (obol.stack) is the real security boundary; the port is
 * transport plumbing. k3d binds to whatever port is free on the Mac host —
 * when 80/443 are already in use it falls back to a dynamic high port
 * (e.g. 64452), so any allowlist of fixed ports breaks the very same
 * UI buttons that are calling these endpoints. We accept any port on
 * hostname obol.stack (HTTP or HTTPS) and reject every other hostname.
 *
 * Override via OBOL_FRONTEND_ALLOWED_ORIGINS (comma-separated, exact match)
 * for operators that need to pin specific origins.
 */
const ALLOWED_HOSTNAME = 'obol.stack';
const ALLOWED_PROTOCOLS = new Set(['http:', 'https:']);

function overrideOrigins(): Set<string> | null {
  const env = process.env.OBOL_FRONTEND_ALLOWED_ORIGINS;
  if (!env) return null;
  return new Set(
    env
      .split(',')
      .map(o => o.trim())
      .filter(Boolean)
  );
}

/**
 * isAllowedOrigin parses the candidate Origin string and returns true if
 * the hostname is obol.stack and the scheme is http/https. Any port (or
 * no port) is accepted. Returns false on parse errors so a malformed
 * Origin can never accidentally allow a request.
 *
 * When OBOL_FRONTEND_ALLOWED_ORIGINS is set the explicit exact-match
 * allowlist takes over — operators get the previous restrictive
 * behavior if they need to pin specific origins.
 */
export function isAllowedOrigin(origin: string): boolean {
  const override = overrideOrigins();
  if (override) return override.has(origin);
  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    return false;
  }
  if (!ALLOWED_PROTOCOLS.has(url.protocol)) return false;
  return url.hostname === ALLOWED_HOSTNAME;
}

function jsonResponse(body: unknown, status: number): NextResponse {
  // Use the standard Response constructor instead of NextResponse.json so
  // this helper has no runtime dependency on `next/server` — that lets the
  // unit test run under plain Node without Next's module resolver. Next.js
  // route handlers accept any Response subclass, so this is functionally
  // identical to NextResponse.json from the caller's perspective.
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }) as NextResponse;
}

/**
 * Returns NextResponse(403) if the request's Origin header isn't allowed.
 * Returns null if the request passes — caller continues.
 *
 * Same-origin GETs/HEADs aren't checked: browsers don't send Origin on
 * navigations and they're safe by definition.
 */
export function rejectCrossOrigin(
  request: NextRequest | Request
): NextResponse | null {
  return rejectCrossOriginWithMode(request, false);
}

/**
 * Like rejectCrossOrigin, but for GET/HEAD routes that return credentials or
 * other sensitive bearer material. Those routes are reads, but not public
 * reads, so they must prove they came from the local obol.stack frontend
 * before any secret is loaded from Kubernetes.
 */
export function rejectCrossOriginCredentialRead(
  request: NextRequest | Request
): NextResponse | null {
  return rejectCrossOriginWithMode(request, true);
}

function rejectCrossOriginWithMode(
  request: NextRequest | Request,
  protectReadMethods: boolean
): NextResponse | null {
  const method = request.method.toUpperCase();
  if (
    method === 'OPTIONS' ||
    (!protectReadMethods && (method === 'GET' || method === 'HEAD'))
  ) {
    return null;
  }
  const origin = request.headers.get('origin');
  if (!origin) {
    // Modern browsers always send Origin on mutating requests. Absence is
    // suspicious (curl, fetch with credentials:'omit', or stripped header).
    // Allow only if Referer parses to an allowed origin as a fallback.
    const referer = request.headers.get('referer');
    if (referer) {
      try {
        const refUrl = new URL(referer);
        const refOrigin = `${refUrl.protocol}//${refUrl.host}`;
        if (isAllowedOrigin(refOrigin)) return null;
      } catch {
        // fall through to 403
      }
    }
    return jsonResponse(
      { error: 'cross-origin request rejected: missing Origin header' },
      403
    );
  }
  if (!isAllowedOrigin(origin)) {
    return jsonResponse(
      {
        error: `cross-origin request rejected: Origin ${origin} not allowed`,
      },
      403
    );
  }
  return null;
}
