export function PaymentFlow() {
  return (
    <details className="rounded-lg border border-stroke bg-bg02 group">
      <summary className="cursor-pointer p-4 text-sm font-medium text-text-light list-none flex items-center justify-between hover:bg-bg03 rounded-lg">
        <span>How x402 payments work</span>
        <span className="text-text-muted group-open:rotate-180 transition-transform">
          ↓
        </span>
      </summary>
      <div className="px-4 pb-4 text-sm text-text-body space-y-3 leading-relaxed">
        <p>
          A request without payment returns HTTP 402 with the price and a
          payment recipe. The buyer (or their library) signs an off-chain
          authorization, retries the request with an{" "}
          <code className="font-mono text-obol-green text-xs">X-PAYMENT</code>{" "}
          header, and the seller&apos;s facilitator settles on-chain. No gas
          needed — the seller covers settlement.
        </p>
        <p>
          Useful for: data feeds, AI inference, subscriptions, digital
          purchases, and agent-to-agent commerce.
        </p>
        <p className="text-xs text-text-muted">
          Spec:{" "}
          <a
            href="https://www.x402.org"
            className="text-obol-green hover:underline"
            target="_blank"
            rel="noopener noreferrer"
          >
            x402.org
          </a>{" "}
          · Python SDK:{" "}
          <a
            href="https://pypi.org/project/x402/"
            className="text-obol-green hover:underline"
            target="_blank"
            rel="noopener noreferrer"
          >
            pypi.org/project/x402
          </a>
        </p>
      </div>
    </details>
  );
}
