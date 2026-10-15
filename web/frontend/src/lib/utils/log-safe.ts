/**
 * logSafe renders a request-derived value for a log line: control
 * characters (newlines included) are stripped so a caller can't forge
 * extra log entries, and long values are cut.
 */
export function logSafe(value: unknown): string {
  return String(value).replace(/[\u0000-\u001f\u007f]/g, '').slice(0, 200);
}
