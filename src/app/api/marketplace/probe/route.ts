import { NextRequest, NextResponse } from 'next/server';
import { rejectCrossOrigin } from '@/lib/middleware/origin-check';
import {
  chatProbeBody,
  parseProbeResponse,
  probeModelForOffer,
} from '@/lib/marketplace/x402-probe';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

const PROBE_TIMEOUT_MS = 15_000;

function isAllowedProbeHost(hostname: string): boolean {
  const h = hostname.toLowerCase();
  if (h === 'obol.stack') return true;
  if (h.endsWith('.trycloudflare.com')) return true;
  if (h === 'traefik.traefik.svc.cluster.local') return true;
  if (h === '127.0.0.1' || h === 'localhost') return true;
  return false;
}

function isAllowedProbeUrl(raw: string): boolean {
  try {
    const u = new URL(raw);
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return false;
    if (!u.pathname.includes('/services/')) return false;
    return isAllowedProbeHost(u.hostname);
  } catch {
    return false;
  }
}

/**
 * POST /api/marketplace/probe
 * Unpaid request to a sell route — expect HTTP 402 with x402 accepts (same as buy.py probe).
 */
export async function POST(request: NextRequest) {
  const csrfReject = rejectCrossOrigin(request);
  if (csrfReject) return csrfReject;

  try {
    const body = await request.json();
    const url = String(body.url ?? '').trim();
    const method = body.method === 'GET' ? 'GET' : 'POST';
    const modelName =
      typeof body.model === 'string' && body.model.trim()
        ? body.model.trim()
        : probeModelForOffer({
            name: String(body.name ?? 'probe'),
            model: body.offerModel,
            agentResolution: body.agentResolution,
          });

    if (!url) {
      return NextResponse.json({ error: 'url is required' }, { status: 400 });
    }
    if (!isAllowedProbeUrl(url)) {
      return NextResponse.json(
        { error: 'Probe URL must be a local or tunnel /services/ route' },
        { status: 400 }
      );
    }

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS);

    const init: RequestInit = {
      method,
      signal: controller.signal,
      headers: { 'content-type': 'application/json' },
    };
    if (method === 'POST') {
      init.body = JSON.stringify(body.requestBody ?? chatProbeBody(modelName));
    }

    let response: Response;
    try {
      response = await fetch(url, init);
    } finally {
      clearTimeout(timer);
    }

    const text = await response.text();
    const result = parseProbeResponse(response.status, text);
    return NextResponse.json(result);
  } catch (error) {
    const message =
      error instanceof Error ? error.message : 'Probe request failed';
    return NextResponse.json(
      {
        ok: false,
        httpStatus: 0,
        gated: false,
        summary: message,
        acceptsCount: 0,
        error: message,
      },
      { status: 502 }
    );
  }
}
