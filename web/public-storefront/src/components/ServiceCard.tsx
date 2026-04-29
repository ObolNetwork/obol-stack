"use client";

import { useState } from "react";
import type { Service } from "@/types";

const typeLabels: Record<string, string> = {
  inference: "Inference",
  http: "HTTP Service",
  "fine-tuning": "Fine-tuning",
};

const typeColors: Record<string, string> = {
  inference: "bg-obol-green/20 text-obol-green",
  http: "bg-obol-blue/40 text-obol-text",
  "fine-tuning": "bg-amber-900/30 text-obol-amber",
};

export function ServiceCard({ service }: { service: Service }) {
  const [showSnippet, setShowSnippet] = useState(false);

  return (
    <div className="rounded-lg border border-obol-border bg-obol-bg-card p-5 transition-colors hover:border-obol-green/40">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 mb-1">
            <h3 className="text-lg font-semibold text-obol-text truncate">
              {service.name}
            </h3>
            {service.isDemo && (
              <span className="shrink-0 rounded px-1.5 py-0.5 text-xs bg-obol-green/15 text-obol-green border border-obol-green/30">
                demo
              </span>
            )}
          </div>
          <p className="text-sm text-obol-muted mb-3">{service.description}</p>
        </div>
        <span
          className={`shrink-0 rounded-full px-2.5 py-0.5 text-xs font-medium ${typeColors[service.type] ?? typeColors.http}`}
        >
          {typeLabels[service.type] ?? service.type}
        </span>
      </div>

      <div className="grid grid-cols-2 gap-x-4 gap-y-1.5 text-sm mb-4">
        <div>
          <span className="text-obol-muted">Price</span>
          <p className="text-obol-text font-mono text-xs">{service.price}</p>
        </div>
        <div>
          <span className="text-obol-muted">Network</span>
          <p className="text-obol-text font-mono text-xs">{service.network}</p>
        </div>
        {service.model && (
          <div className="col-span-2">
            <span className="text-obol-muted">Model</span>
            <p className="text-obol-text font-mono text-xs">
              {service.model}
            </p>
          </div>
        )}
        <div className="col-span-2">
          <span className="text-obol-muted">Endpoint</span>
          <p className="text-obol-green font-mono text-xs break-all">
            {service.endpoint}
          </p>
        </div>
      </div>

      <button
        onClick={() => setShowSnippet(!showSnippet)}
        className="text-xs text-obol-green hover:text-obol-green/80 font-medium cursor-pointer"
      >
        {showSnippet ? "Hide" : "Show"} code snippet
      </button>

      {showSnippet && (
        <div className="mt-3 space-y-3">
          <SnippetBlock
            title="Probe pricing (curl)"
            code={`curl -s ${service.endpoint} | jq .`}
          />
          <SnippetBlock
            title="Paid request (Python)"
            code={`import httpx
from x402.client import x402_client

client = x402_client(
    httpx.Client(),
    private_key="<your-private-key>",
)
resp = client.get("${service.endpoint}")
print(resp.json())`}
          />
          <SnippetBlock
            title="Agent prompt"
            code={`Call the paid service at ${service.endpoint} using x402 payment. It costs ${service.price} on ${service.network}. Report what it returns.`}
          />
        </div>
      )}
    </div>
  );
}

function SnippetBlock({ title, code }: { title: string; code: string }) {
  const [copied, setCopied] = useState(false);

  const copy = () => {
    navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <span className="text-xs text-obol-muted">{title}</span>
        <button
          onClick={copy}
          className="text-xs text-obol-muted hover:text-obol-green cursor-pointer"
        >
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre className="rounded bg-obol-bg p-3 text-xs font-mono text-obol-text overflow-x-auto border border-obol-border">
        {code}
      </pre>
    </div>
  );
}
