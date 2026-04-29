export function PaymentFlow() {
  return (
    <div className="rounded-lg border border-obol-border bg-obol-bg-card p-5">
      <h2 className="text-base font-semibold text-obol-text mb-3">
        How x402 Payment Works
      </h2>
      <ol className="space-y-2 text-sm text-obol-muted list-decimal list-inside">
        <li>
          <span className="text-obol-text">Send a request</span> to any
          service endpoint without payment headers
        </li>
        <li>
          Receive <span className="text-obol-text">HTTP 402</span> with{" "}
          <code className="text-obol-green font-mono text-xs">accepts</code>{" "}
          array containing payment requirements (scheme, network, amount, asset,
          payTo)
        </li>
        <li>
          <span className="text-obol-text">Sign an ERC-3009</span>{" "}
          TransferWithAuthorization off-chain with your wallet
        </li>
        <li>
          Base64-encode the signed payload and attach it as an{" "}
          <code className="text-obol-green font-mono text-xs">X-PAYMENT</code>{" "}
          header
        </li>
        <li>
          Resend the request &mdash; the{" "}
          <span className="text-obol-text">x402 facilitator</span> verifies and
          settles on-chain
        </li>
        <li>
          Receive the <span className="text-obol-text">service response</span>{" "}
          with settlement receipt in{" "}
          <code className="text-obol-green font-mono text-xs">
            X-PAYMENT-RESPONSE
          </code>
        </li>
      </ol>
      <p className="mt-3 text-xs text-obol-muted">
        The{" "}
        <a
          href="https://pypi.org/project/x402/"
          className="text-obol-green hover:underline"
          target="_blank"
          rel="noopener noreferrer"
        >
          x402 Python SDK
        </a>{" "}
        handles steps 2-5 automatically. See{" "}
        <a
          href="https://www.x402.org"
          className="text-obol-green hover:underline"
          target="_blank"
          rel="noopener noreferrer"
        >
          x402.org
        </a>{" "}
        for the full protocol specification.
      </p>
    </div>
  );
}
