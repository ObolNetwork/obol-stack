// buy.py's default per-call ceiling for one-shot `go` / `pay` / `pay-agent`,
// in whole token units. Mirrors buyprompts.DefaultMaxPrice (Go) and
// DEFAULT_MAX_PRICE in buy-x402/scripts/buy.py.
export const DEFAULT_MAX_PRICE = 1;

// maxPriceFlag returns " --max-price <price>" when a per-request price (a
// plain decimal in whole token units, e.g. "2.5") exceeds buy.py's default
// per-call ceiling, else "". Compared on the decimal string itself, so no
// floating-point rounding. Only used for the fallback prompt when the
// catalog carries no `buy` block; catalog prompts already include the flag.
export function maxPriceFlag(price?: string, priceUnit?: string): string {
  if (!price || (priceUnit && priceUnit !== "perRequest")) return "";
  const p = price.trim();
  const m = /^(\d+)(?:\.(\d+))?$/.exec(p);
  if (!m) return "";
  const whole = BigInt(m[1]);
  const fracNonZero = /[1-9]/.test(m[2] ?? "");
  const ceiling = BigInt(DEFAULT_MAX_PRICE);
  const exceeds = whole > ceiling || (whole === ceiling && fracNonZero);
  return exceeds ? ` --max-price ${p}` : "";
}
