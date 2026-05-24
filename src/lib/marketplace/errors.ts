/**
 * Typed error for "I tried to talk to the Obol Stack and could not reach
 * it." Used to drive a single, consistent "stack unreachable" surface
 * across the marketplace and dashboard UIs.
 *
 * Heuristic: anything that smells like a non-running stack — fetch
 * TypeError, AbortError, HTTP 5xx — gets wrapped in this. HTTP 4xx
 * (404 missing slug, 400 bad filter) is NOT unreachable; those are
 * surfaced as ordinary errors.
 */

export const STACK_UNREACHABLE = 'STACK_UNREACHABLE' as const;

export class StackUnreachableError extends Error {
  readonly code = STACK_UNREACHABLE;
  /** HTTP status when the failure was an HTTP response (0 for network). */
  readonly status: number;
  constructor(message: string, status = 0, options?: ErrorOptions) {
    super(message, options);
    this.name = 'StackUnreachableError';
    this.status = status;
  }
}

export function isStackUnreachable(
  error: unknown
): error is StackUnreachableError {
  return (
    error instanceof Error &&
    (error as { code?: string }).code === STACK_UNREACHABLE
  );
}

/**
 * Classify a fetch failure or non-OK response into either a
 * StackUnreachableError (cluster is down / 5xx) or a generic Error
 * (anything else, e.g. 404 or 400).
 *
 * `responseStatus` is `0` when the underlying error was a network failure
 * (no response at all). For HTTP errors pass the actual status code.
 */
export function classifyFetchFailure(
  error: unknown,
  responseStatus = 0
): Error {
  // Network-level failures: TypeError ("failed to fetch"), AbortError,
  // DOMException. All point at "the host on the other end didn't answer".
  if (responseStatus === 0 || responseStatus >= 500) {
    const detail = responseStatus > 0 ? ` (HTTP ${responseStatus})` : '';
    return new StackUnreachableError(
      `Obol Stack unreachable${detail}`,
      responseStatus,
      { cause: error }
    );
  }
  if (error instanceof Error) return error;
  return new Error(String(error));
}
