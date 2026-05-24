import type { NextRequest, NextResponse } from 'next/server';

/**
 * Allowed origins for mutating requests. The frontend is exposed only via
 * the Traefik HTTPRoute hostname-restricted to "obol.stack", so the only
 * legitimate Origin header value is the obol.stack URL.
 *
 * Without this check, a malicious page on the same machine (another browser
 * tab pointed at evil.com) can submit a form POST cross-origin to
 * /api/sell/create — the browser blocks reading the response (SOP) but
 * does NOT block the side effect of creating a ServiceOffer.
 *
 * Override via OBOL_FRONTEND_ALLOWED_ORIGINS (comma-separated) for dev.
 */
function allowedOrigins(): Set<string> {
  const env = process.env.OBOL_FRONTEND_ALLOWED_ORIGINS;
  if (env) return new Set(env.split(',').map(o => o.trim()));
  return new Set(['http://obol.stack:8080', 'https://obol.stack:8443']);
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
 * Returns NextResponse(403) if the request's Origin header isn't in the
 * allowlist. Returns null if the request passes — caller continues.
 *
 * Same-origin GETs/HEADs aren't checked: browsers don't send Origin on
 * navigations and they're safe by definition.
 */
export function rejectCrossOrigin(
  request: NextRequest | Request
): NextResponse | null {
  const method = request.method.toUpperCase();
  if (method === 'GET' || method === 'HEAD' || method === 'OPTIONS') {
    return null;
  }
  const origin = request.headers.get('origin');
  if (!origin) {
    // Modern browsers always send Origin on mutating requests. Absence is
    // suspicious (curl, fetch with credentials:'omit', or stripped header).
    // Allow only if Referer matches an allowed origin's prefix as a fallback.
    const referer = request.headers.get('referer');
    if (referer) {
      for (const allowed of allowedOrigins()) {
        if (referer.startsWith(allowed)) return null;
      }
    }
    return jsonResponse(
      { error: 'cross-origin request rejected: missing Origin header' },
      403
    );
  }
  if (!allowedOrigins().has(origin)) {
    return jsonResponse(
      {
        error: `cross-origin request rejected: Origin ${origin} not allowed`,
      },
      403
    );
  }
  return null;
}
