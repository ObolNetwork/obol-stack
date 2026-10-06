/**
 * Strict request-body validation for POST /api/sell/create.
 *
 * Pure (no next/server import) so node:test can exercise it directly.
 * Everything that ends up in the ServiceOffer CR — and therefore in the
 * x402 verifier route, the upstream Service lookup and the health probe —
 * is constrained here before the route touches the Kubernetes API.
 */
import { Type, type Static } from '@sinclair/typebox';
import { Value } from '@sinclair/typebox/value';

/** RFC 1123 / DNS-1123 label: Kubernetes object + Service names. */
export const DNS1123_LABEL = '^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$';
export const EVM_ADDRESS = '^0x[0-9a-fA-F]{40}$';
/** Positive decimal string; zero is rejected separately. */
export const DECIMAL = '^(0|[1-9][0-9]{0,17})(\\.[0-9]{1,18})?$';
/** Absolute URL path: leading slash, unreserved + sub-delim chars only. */
export const HEALTH_PATH = "^/[A-Za-z0-9\\-._~!$&'()*+,;=:@/%]{0,255}$";
/** Model ids such as `qwen3.5:4b`, `openai/gpt-4o`, `llama-3.1-8b@q4`. */
export const MODEL_ID = '^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,252}$';

const Label = (description: string) =>
  Type.String({ pattern: DNS1123_LABEL, maxLength: 63, description });

export const CreateSellRequestSchema = Type.Object(
  {
    name: Label('ServiceOffer name (DNS-1123 label)'),
    namespace: Type.Optional(Label('target namespace (DNS-1123 label)')),
    type: Type.Optional(
      Type.Union([
        Type.Literal('http'),
        Type.Literal('inference'),
        Type.Literal('agent'),
        Type.Literal('fine-tuning'),
      ])
    ),
    wallet: Type.String({ pattern: EVM_ADDRESS }),
    chain: Label('payment chain id'),
    token: Type.Optional(
      Type.Union([Type.Literal('USDC'), Type.Literal('OBOL')])
    ),
    pricePerRequest: Type.String({ pattern: DECIMAL, maxLength: 40 }),
    upstream: Type.Optional(Label('upstream Service name')),
    port: Type.Optional(Type.Integer({ minimum: 1, maximum: 65535 })),
    healthPath: Type.Optional(
      Type.String({ pattern: HEALTH_PATH, maxLength: 256 })
    ),
    model: Type.Optional(Type.String({ pattern: MODEL_ID, maxLength: 253 })),
    agentRef: Type.Optional(
      Type.Object(
        {
          name: Type.Optional(Label('agent name')),
          namespace: Type.Optional(Label('agent namespace')),
        },
        { additionalProperties: false }
      )
    ),
    skills: Type.Optional(
      Type.Array(
        Type.String({
          minLength: 1,
          maxLength: 128,
          pattern: '^[^\\x00-\\x1f\\x7f]+$',
        }),
        { maxItems: 64 }
      )
    ),
    description: Type.Optional(
      Type.String({
        maxLength: 2000,
        pattern: '^[^\\x00-\\x08\\x0b\\x0c\\x0e-\\x1f\\x7f]*$',
      })
    ),
  },
  { additionalProperties: false }
);

export type CreateSellRequest = Static<typeof CreateSellRequestSchema>;

export type CreateSellParseResult =
  { ok: true; value: CreateSellRequest } | { ok: false; error: string };

const FIELD_HINTS: Record<string, string> = {
  name: 'must be a DNS-1123 label (lowercase a-z, 0-9, "-", max 63 chars)',
  namespace: 'must be a DNS-1123 label',
  type: 'must be one of http, inference, agent, fine-tuning',
  wallet: 'must be a 0x-prefixed 40-hex-character address',
  chain: 'must be a chain id such as base or base-sepolia',
  token: 'must be USDC or OBOL',
  pricePerRequest: 'must be a positive decimal string (e.g. "0.001")',
  upstream: 'must be a DNS-1123 label (Kubernetes Service name)',
  port: 'must be an integer between 1 and 65535',
  healthPath: 'must start with "/" and contain only URL path characters',
  model: 'must be a model id (letters, digits, . _ : / @ + -)',
  agentRef: 'must be { name, namespace } with DNS-1123 labels',
  skills: 'must be an array of up to 64 non-empty strings',
  description: 'must be at most 2000 characters of printable text',
};

const REQUIRED = ['name', 'wallet', 'chain', 'pricePerRequest'] as const;
const KNOWN = new Set(Object.keys(CreateSellRequestSchema.properties));

function fieldOf(path: string): string {
  return path.split('/').filter(Boolean)[0] ?? '';
}

export function parseCreateSellRequest(body: unknown): CreateSellParseResult {
  if (!body || typeof body !== 'object' || Array.isArray(body)) {
    return { ok: false, error: 'request body must be a JSON object' };
  }

  const record = body as Record<string, unknown>;
  const missing = REQUIRED.filter(
    k => record[k] === undefined || record[k] === null || record[k] === ''
  );
  if (missing.length > 0) {
    return {
      ok: false,
      error: `Missing required fields: ${missing.join(', ')}`,
    };
  }
  const unknown = Object.keys(record).filter(k => !KNOWN.has(k));
  if (unknown.length > 0) {
    return { ok: false, error: `Unknown field(s): ${unknown.join(', ')}` };
  }

  const first = Value.Errors(CreateSellRequestSchema, body).First();
  if (first) {
    const field = fieldOf(first.path);
    const hint = FIELD_HINTS[field] ?? first.message;
    return { ok: false, error: field ? `Invalid ${field}: ${hint}` : hint };
  }

  const value = body as CreateSellRequest;
  if (!/[1-9]/.test(value.pricePerRequest)) {
    return {
      ok: false,
      error: `Invalid pricePerRequest: ${FIELD_HINTS.pricePerRequest}`,
    };
  }
  return { ok: true, value };
}
