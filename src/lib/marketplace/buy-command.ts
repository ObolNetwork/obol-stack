/**
 * Pure helpers that build the shell snippets the BuyDrawer surfaces. These
 * never run in the browser — they only render as copy-to-clipboard text — so
 * we can keep them deterministic and unit-test them without mocking k8s or
 * the agent pod.
 *
 * Three forms are surfaced in the drawer:
 *
 * 1. `buy.py buy …`  — what the agent invokes inside the pod
 * 2. `kubectl exec …` — host-side wrapper that calls (1) via the cluster
 * 3. `curl …`        — paid LiteLLM round-trip the user can run from any
 *                       OpenAI-compatible client once the sidecar has
 *                       pre-signed auths
 */

import type { ServiceListItem } from './types';

export interface BuyPreflight {
  count: number;
  /** Decimal-form total: count × item.price (string-safe). */
  totalHuman: string;
  /** Atomic-units total: count × item.priceAtomicUnits (BigInt-safe). */
  totalAtomic: string;
}

const DEFAULT_NAME_FALLBACK = 'svc';

/**
 * Compute the buy preflight totals. Uses BigInt for atomic units so we can
 * multiply huge OBOL/wei numbers without precision loss; the human total
 * is decimal-string * count formatted to the same number of fractional
 * digits as `price` (so "0.001" × 5 = "0.005", not "0.005000").
 */
export function computePreflight(
  item: Pick<ServiceListItem, 'price' | 'priceAtomicUnits'>,
  count: number
): BuyPreflight {
  const n = Math.max(1, Math.floor(count));
  return {
    count: n,
    totalHuman: multiplyDecimalString(item.price || '0', n),
    totalAtomic: multiplyAtomicString(item.priceAtomicUnits || '0', n),
  };
}

/**
 * Build the agent-side `buy.py buy` command. This is what the agent shell
 * receives, not the kubectl wrapper.
 */
export function buyPyCommand(
  item: Pick<ServiceListItem, 'name' | 'endpoint' | 'modelOrRuntime'> & {
    remoteModel?: string;
  },
  count: number
): string {
  const name = item.name || DEFAULT_NAME_FALLBACK;
  const remote =
    item.remoteModel || extractRemoteModel(item.modelOrRuntime) || 'auto';
  return [
    'buy.py buy',
    quoteArg(name),
    `--endpoint ${quoteArg(item.endpoint)}`,
    `--model ${quoteArg(remote)}`,
    `--count ${Math.max(1, Math.floor(count))}`,
  ].join(' \\\n    ');
}

/**
 * Build the host-side kubectl exec wrapper that drives the in-pod buy.py.
 * Uses the canonical LiteLLM pod label selector — no jsonpath lookup is
 * needed because k8s expands `-l app=litellm` server-side.
 */
export function kubectlBuyCommand(
  item: Pick<ServiceListItem, 'name' | 'endpoint' | 'modelOrRuntime'> & {
    remoteModel?: string;
  },
  count: number
): string {
  const name = item.name || DEFAULT_NAME_FALLBACK;
  const remote =
    item.remoteModel || extractRemoteModel(item.modelOrRuntime) || 'auto';
  return [
    'kubectl -n llm exec -it deploy/litellm -c x402-buyer --',
    'buy.py buy',
    quoteArg(name),
    `--endpoint ${quoteArg(item.endpoint)}`,
    `--model ${quoteArg(remote)}`,
    `--count ${Math.max(1, Math.floor(count))}`,
  ].join(' \\\n    ');
}

/**
 * Build the LiteLLM-side curl example. The route LiteLLM publishes after
 * a successful buy is `paid/<remote-model>`, so this snippet is the
 * "validate it worked" round-trip the drawer surfaces alongside the buy.
 */
export function litellmCurlExample(
  item: Pick<ServiceListItem, 'modelOrRuntime'> & { remoteModel?: string }
): string {
  const remote =
    item.remoteModel || extractRemoteModel(item.modelOrRuntime) || 'auto';
  const body = JSON.stringify(
    {
      model: `paid/${remote}`,
      messages: [{ role: 'user', content: 'ping' }],
    },
    null,
    2
  );
  return [
    'curl -sS http://litellm.llm.svc.cluster.local:4000/v1/chat/completions \\',
    "    -H 'content-type: application/json' \\",
    `    -d '${body.replace(/\n/g, ' ')}'`,
  ].join('\n');
}

function extractRemoteModel(modelOrRuntime: string): string | undefined {
  // Agent rows render as "runtime · model"; inference rows render as just
  // "model". Recover the model side either way.
  if (!modelOrRuntime) return undefined;
  const parts = modelOrRuntime.split('·').map(s => s.trim());
  return parts[parts.length - 1] || undefined;
}

function quoteArg(value: string | undefined): string {
  if (!value) return "''";
  // Single-quote and escape embedded single quotes the bash-safe way.
  return `'${value.replace(/'/g, "'\\''")}'`;
}

/**
 * Multiply a decimal string by a positive integer, preserving the input's
 * fractional digit count. "0.001" × 5 → "0.005".
 */
export function multiplyDecimalString(value: string, count: number): string {
  const n = Math.max(0, Math.floor(count));
  if (n === 0) return '0';
  const [intPart, fracPart = ''] = value.split('.');
  const digits = fracPart.length;
  const scaled = BigInt(intPart + fracPart) * BigInt(n);
  const scaledStr = scaled.toString();
  if (digits === 0) return scaledStr;
  const padded = scaledStr.padStart(digits + 1, '0');
  const intOut = padded.slice(0, padded.length - digits);
  const fracOut = padded.slice(padded.length - digits);
  return `${intOut}.${fracOut}`;
}

/**
 * Multiply an atomic-unit integer string by a positive integer. Always
 * integer math; no decimals involved.
 */
export function multiplyAtomicString(value: string, count: number): string {
  if (!value) return '0';
  const n = Math.max(0, Math.floor(count));
  if (n === 0) return '0';
  return (BigInt(value) * BigInt(n)).toString();
}
