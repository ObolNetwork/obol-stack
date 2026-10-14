/**
 * Pure helpers for /api/sell/stop. Kept free of next/server so node:test can
 * exercise validation + patch construction without instantiating a Next
 * request context.
 */

export interface StopRequestInput {
  name?: unknown;
  namespace?: unknown;
  grace?: unknown;
  force?: unknown;
}

export interface StopRequest {
  name: string;
  namespace: string;
  grace: string;
  force: boolean;
}

export type ParseResult =
  | { ok: true; value: StopRequest }
  | { ok: false; error: string };

const DEFAULT_GRACE = '1h';
const FORCE_GRACE = '0s';

// Matches Go time.ParseDuration: digits + optional fractional + unit.
// Units: ns, us (or µs), ms, s, m, h. Concatenated terms allowed (e.g. "1h30m").
const DURATION_RE = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;

// K8s name: RFC 1123 label, kept conservative.
const K8S_NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export function isValidGoDuration(value: string): boolean {
  if (!value) return false;
  if (value.length > 32) return false;
  return DURATION_RE.test(value);
}

export function parseStopRequest(body: StopRequestInput): ParseResult {
  if (!body || typeof body !== 'object') {
    return { ok: false, error: 'body must be an object' };
  }

  if (typeof body.name !== 'string' || !K8S_NAME_RE.test(body.name)) {
    return { ok: false, error: 'name is required and must be a valid label' };
  }
  if (typeof body.namespace !== 'string' || !K8S_NAME_RE.test(body.namespace)) {
    return {
      ok: false,
      error: 'namespace is required and must be a valid label',
    };
  }

  const force = body.force === true;

  let grace: string;
  if (force) {
    grace = FORCE_GRACE;
  } else if (
    body.grace === undefined ||
    body.grace === null ||
    body.grace === ''
  ) {
    grace = DEFAULT_GRACE;
  } else if (typeof body.grace === 'string' && isValidGoDuration(body.grace)) {
    grace = body.grace;
  } else {
    return {
      ok: false,
      error: 'grace must be a Go duration string (e.g. "1h", "30m", "5m")',
    };
  }

  return {
    ok: true,
    value: { name: body.name, namespace: body.namespace, grace, force },
  };
}

export interface DrainPatch {
  spec: {
    drainAt: string;
    drainGracePeriod: string;
  };
}

/**
 * Parses a Go duration into milliseconds. Supports the unit set permitted by
 * `isValidGoDuration`. Used to compute drainEndsAt for the response so the UI
 * can show a countdown without round-tripping to the controller.
 */
export function goDurationToMs(value: string): number {
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let total = 0;
  let match: RegExpExecArray | null;
  while ((match = re.exec(value)) !== null) {
    const n = parseFloat(match[1]);
    switch (match[2]) {
      case 'ns':
        total += n / 1_000_000;
        break;
      case 'us':
      case 'µs':
        total += n / 1_000;
        break;
      case 'ms':
        total += n;
        break;
      case 's':
        total += n * 1_000;
        break;
      case 'm':
        total += n * 60_000;
        break;
      case 'h':
        total += n * 3_600_000;
        break;
    }
  }
  return total;
}

export function buildDrainPatch(
  req: StopRequest,
  now: Date
): { patch: DrainPatch; drainEndsAt: Date } {
  const drainAt = now.toISOString();
  const drainEndsAt = new Date(now.getTime() + goDurationToMs(req.grace));
  return {
    patch: {
      spec: {
        drainAt,
        drainGracePeriod: req.grace,
      },
    },
    drainEndsAt,
  };
}
