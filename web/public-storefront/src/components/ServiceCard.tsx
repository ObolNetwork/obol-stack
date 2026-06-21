"use client";

import { useState } from "react";
import type { Service, ServicePayment } from "@/types";

// paymentOptions returns the service's accepted payment options, falling back
// to the flat fields for catalogs predating multi-currency.
function paymentOptions(service: Service): ServicePayment[] {
  if (service.payments && service.payments.length > 0) return service.payments;
  return [
    {
      price: service.price,
      priceRaw: service.priceRaw,
      payTo: service.payTo,
      network: service.network,
      asset: service.asset,
    },
  ];
}

function optionLabel(opt: ServicePayment): string {
  const sym = opt.asset?.symbol ?? "USDC";
  return `${opt.price.replace(/\s.*/, "")} ${sym} · ${opt.network}`;
}

// docsRef points a foreign agent at THIS operator's own self-contained docs
// (served over the same tunnel as the endpoint) instead of the broad
// obol.org/llms.txt: /skill.md carries the full x402 payment flow and
// /openapi.json the exact request shapes. Falls back to a generic x402
// pointer if the endpoint origin can't be parsed.
function docsRef(endpoint: string): string {
  try {
    const origin = new URL(endpoint).origin;
    return `Read ${origin}/skill.md for the x402 payment flow and ${origin}/openapi.json for the exact request shapes.`;
  } catch {
    return "See https://www.x402.org for how x402 micropayments work.";
  }
}

const typeColors: Record<string, string> = {
  inference: "bg-obol-green/15 text-obol-green border border-obol-green/30",
  agent: "bg-obol-green/15 text-obol-green border border-obol-green/30",
  http: "bg-bg03 text-text-body border border-stroke",
  "fine-tuning": "bg-amber/15 text-amber border border-amber/30",
};

// normalizeOfferType collapses the catalog's spec.type values into the
// three branches the storefront renders. Mirrors the Go renderer's
// normalizeOfferType in internal/x402/paymentrequired.go so the
// storefront and 402 page agree on what each card surfaces.
function normalizeOfferType(t: string): "inference" | "agent" | "http" {
  if (t === "inference" || t === "agent") return t;
  return "http";
}

type Tab = "agent" | "other-ai" | "code";

export function ServiceCard({ service }: { service: Service }) {
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<Tab>("agent");
  const [copied, setCopied] = useState(false);

  const options = paymentOptions(service);
  const [optIdx, setOptIdx] = useState(0);
  const opt = options[optIdx] ?? options[0];
  const multiPay = options.length > 1;

  const anchorId = `service-${service.name}`;
  const copyAnchor = () => {
    const url = `${window.location.origin}${window.location.pathname}#${anchorId}`;
    navigator.clipboard.writeText(url);
    window.location.hash = anchorId;
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div
      id={anchorId}
      className="scroll-mt-4 rounded-lg border border-stroke bg-bg02 p-5 transition-colors hover:bg-bg03"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 mb-1">
            <h3 className="text-lg font-semibold text-text-light truncate">
              {service.name}
            </h3>
            <button
              onClick={copyAnchor}
              title="Copy link to this service"
              aria-label="Copy link to this service"
              className="shrink-0 text-text-muted hover:text-obol-green text-sm cursor-pointer font-mono"
            >
              {copied ? "✓" : "#"}
            </button>
            {service.category && (
              <span className="shrink-0 rounded px-1.5 py-0.5 text-xs bg-obol-green/15 text-obol-green border border-obol-green/30">
                {service.category}
              </span>
            )}
          </div>
          <p className="text-sm text-text-body mb-3">{service.description}</p>
          {service.skills && service.skills.length > 0 && (
            <div className="flex flex-wrap gap-1.5 mb-3" aria-label="Agent skills">
              {service.skills.map((s) => (
                <span
                  key={s}
                  className="inline-block rounded-full border border-stroke bg-bg03 px-2.5 py-0.5 text-xs text-text-body"
                >
                  {s}
                </span>
              ))}
            </div>
          )}
        </div>
        <span
          className={`shrink-0 rounded-full px-2.5 py-0.5 text-xs font-medium ${typeColors[service.type] ?? typeColors.http}`}
        >
          {service.type}
        </span>
      </div>

      <div className="grid grid-cols-2 gap-x-4 gap-y-1.5 text-sm mb-4">
        {multiPay ? (
          <div className="col-span-2">
            <span className="text-text-muted">
              Pay with {options.length} options
            </span>
            <ul className="mt-1 space-y-0.5">
              {options.map((o, i) => (
                <li key={`${o.network}-${o.asset?.symbol ?? "USDC"}`} className="text-text-light font-mono text-xs">
                  {optionLabel(o)}
                  {i === optIdx && open ? " ←" : ""}
                </li>
              ))}
            </ul>
          </div>
        ) : (
          <>
            <div>
              <span className="text-text-muted">Price</span>
              <p className="text-text-light font-mono text-xs">{opt.price}</p>
            </div>
            <div>
              <span className="text-text-muted">Network</span>
              <p className="text-text-light font-mono text-xs">{opt.network}</p>
            </div>
          </>
        )}
        {service.model && (
          <div className="col-span-2">
            <span className="text-text-muted">Model</span>
            <p className="text-text-light font-mono text-xs">{service.model}</p>
          </div>
        )}
        <div className="col-span-2">
          <span className="text-text-muted">Endpoint</span>
          <a
            href={service.endpoint}
            className="block text-obol-green font-mono text-xs break-all hover:underline"
          >
            {service.endpoint}
          </a>
        </div>
      </div>

      <button
        onClick={() => setOpen(!open)}
        className="text-sm text-obol-green hover:text-obol-green/80 font-medium cursor-pointer"
      >
        {open ? "Hide options ↑" : "Buy this service ↓"}
      </button>

      {open && (
        <div className="mt-4 space-y-4">
          {multiPay && (
            <div>
              <p className="text-xs text-text-muted mb-1.5">Pay with</p>
              <div className="flex flex-wrap gap-1.5">
                {options.map((o, i) => (
                  <button
                    key={`${o.network}-${o.asset?.symbol ?? "USDC"}`}
                    onClick={() => setOptIdx(i)}
                    className={`rounded border px-2.5 py-1 text-xs font-mono cursor-pointer transition-colors ${
                      i === optIdx
                        ? "border-obol-green text-obol-green bg-obol-green/10"
                        : "border-stroke text-text-body hover:border-obol-green/50"
                    }`}
                  >
                    {optionLabel(o)}
                  </button>
                ))}
              </div>
            </div>
          )}

          <TabBar tab={tab} onChange={setTab} />

          {tab === "agent" && <BuyViaObolAgent service={service} opt={opt} />}
          {tab === "other-ai" && <BuyViaOtherAgent service={service} opt={opt} />}
          {tab === "code" && <BuyWithCode service={service} opt={opt} />}
        </div>
      )}
    </div>
  );
}

function TabBar({ tab, onChange }: { tab: Tab; onChange: (t: Tab) => void }) {
  const tabs: { id: Tab; label: string }[] = [
    { id: "agent", label: "Ask your Obol agent" },
    { id: "other-ai", label: "Ask another AI agent" },
    { id: "code", label: "Buy with code" },
  ];
  return (
    <div className="flex gap-1 border-b border-stroke">
      {tabs.map((t) => {
        const active = t.id === tab;
        return (
          <button
            key={t.id}
            onClick={() => onChange(t.id)}
            className={`px-3 py-2 text-xs font-medium border-b-2 -mb-px transition-colors cursor-pointer ${
              active
                ? "border-obol-green text-obol-green"
                : "border-transparent text-text-body hover:text-text-light"
            }`}
          >
            {t.label}
          </button>
        );
      })}
    </div>
  );
}

// BuyViaObolAgent branches on service.type so the prompt matches what
// the buy-x402 skill actually does for that shape (`pay` for http,
// `pay` against chat-completions for agents, `obol buy inference` CLI
// for inference). Mirrors inferenceCopy/agentCopy/httpCopy in
// internal/x402/paymentrequired.go.
function BuyViaObolAgent({ service, opt }: { service: Service; opt: ServicePayment }) {
  const kind = normalizeOfferType(service.type);

  if (kind === "inference") {
    const model = service.model || "<model-id>";
    const cmd = `obol buy inference ${service.name} \\
  --seller ${service.endpoint} \\
  --model ${model} \\
  --budget 1 \\
  --no-verify-identity`;
    return (
      <div className="space-y-2">
        <p className="text-xs text-text-muted">
          This is paid <em>remote inference</em>. The Obol CLI pre-pays the
          seller through your agent&apos;s wallet and exposes the model in
          your local LiteLLM gateway as{" "}
          <code className="font-mono text-obol-green">paid/{model}</code>, so
          your agent and tools can call it like any other OpenAI-compatible
          model.
        </p>
        <Snippet code={cmd} />
      </div>
    );
  }

  if (kind === "agent") {
    const prompt = `Use the buy-x402 skill's \`pay\` command to call the Obol Agent at ${service.endpoint}. This is an *agent*, not a raw model — it has its own skills, tools, and memory. Include a clear instruction in the chat-completions body so the agent knows what to do.`;
    return (
      <div className="space-y-2">
        <p className="text-xs text-text-muted">
          Paste this into your Obol agent. You&apos;re paying another agent
          for one round of work — be specific about what you want it to do.
          The buy-x402 skill signs and sends the payment for you.
        </p>
        <Snippet code={prompt} />
      </div>
    );
  }

  // http (default): legacy single-shot pay.
  const prompt = `Use the buy-x402 skill's \`pay\` command to call ${service.endpoint} once. Pay ${opt.price} on ${opt.network}. Report what it returns.`;
  return (
    <div className="space-y-2">
      <p className="text-xs text-text-muted">
        Paste this into your Obol agent. The agent uses the built-in{" "}
        <code className="font-mono text-obol-green">buy-x402</code> skill to
        sign one authorisation and call the endpoint.
      </p>
      <Snippet code={prompt} />
    </div>
  );
}

function BuyViaOtherAgent({ service, opt }: { service: Service; opt: ServicePayment }) {
  const kind = normalizeOfferType(service.type);

  let prompt: string;
  if (kind === "inference") {
    const model = service.model || "the advertised model";
    prompt = `${docsRef(service.endpoint)} I want to use the remote LLM at ${service.endpoint} (model ${model}) as a paid OpenAI-compatible chat-completions endpoint. Pre-sign a budget of EIP-3009 or Permit2 authorisations and POST chat-completions bodies with the X-PAYMENT header attached.`;
  } else if (kind === "agent") {
    const modelLine = service.model ? ` (running ${service.model})` : "";
    prompt = `${docsRef(service.endpoint)} Help me call the Obol Agent at ${service.endpoint}${modelLine} — it's an autonomous agent (tools + skills + memory), not a raw LLM. POST OpenAI-style chat-completions JSON with a real prompt in \`messages\`, attach a signed EIP-3009 or Permit2 authorisation as \`X-PAYMENT\`, and report what the agent does.`;
  } else {
    prompt = `I want to purchase a service offered by an Obol Agent at ${service.endpoint} for ${opt.price} on ${opt.network}. Please install the run-obol-stack skill from https://github.com/ObolNetwork/skills, ask me for permission to set up the obol stack, and use the buy-x402 skill to make the purchase on my behalf.`;
  }

  return (
    <div className="space-y-2">
      <p className="text-xs text-text-muted">
        Paste this into Claude, ChatGPT, Gemini, or any AI agent with
        internet access. The buy-x402 skill from{" "}
        <a
          href="https://github.com/ObolNetwork/skills"
          target="_blank"
          rel="noopener noreferrer"
          className="text-obol-green hover:underline"
        >
          ObolNetwork/skills
        </a>{" "}
        bootstraps the stack and asks for your permission before spending.
      </p>
      <Snippet code={prompt} />
    </div>
  );
}

function BuyWithCode({ service, opt }: { service: Service; opt: ServicePayment }) {
  const kind = normalizeOfferType(service.type);
  return (
    <div className="space-y-4">
      <div>
        <h4 className="text-xs font-semibold text-text-light mb-2">
          1. Check the API pricing
        </h4>
        <div className="space-y-2">
          <Snippet code={`curl -s ${service.endpoint} | jq .`} />
          <a
            href={service.endpoint}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-xs text-obol-green hover:underline"
          >
            ↗ View in browser
          </a>
        </div>
      </div>

      <div>
        <h4 className="text-xs font-semibold text-text-light mb-2">
          2. Pay for the service
        </h4>
        <LanguageTabs service={service} opt={opt} />
      </div>

      {kind === "agent" && (
        <div>
          <h4 className="text-xs font-semibold text-text-light mb-2">
            3. Send a prompt (OpenAI chat-completions)
          </h4>
          <p className="text-xs text-text-muted mb-2">
            Obol Agents accept OpenAI-style chat-completions bodies. A
            request like the following will get you an answer:
          </p>
          <Snippet
            code={`POST ${service.endpoint}
Content-Type: application/json
X-PAYMENT: <pre-signed-EIP-3009-or-Permit2-voucher>

{
${service.model ? `  "model": "${service.model}",\n` : ""}  "messages": [
    {"role": "user", "content": "<your prompt to this agent goes here>"}
  ]
}`}
          />
        </div>
      )}

      {kind === "inference" && (
        <div>
          <h4 className="text-xs font-semibold text-text-light mb-2">
            3. Call it as an OpenAI-compatible endpoint
          </h4>
          <p className="text-xs text-text-muted mb-2">
            Once payment is settled, this endpoint accepts standard
            chat-completions requests at{" "}
            <code className="font-mono text-obol-green">
              {service.endpoint}/v1/chat/completions
            </code>
            . The Obol CLI option above also installs it locally as{" "}
            <code className="font-mono text-obol-green">
              paid/{service.model || "<model>"}
            </code>{" "}
            so existing LiteLLM clients pick it up unchanged.
          </p>
        </div>
      )}
    </div>
  );
}

function LanguageTabs({ service, opt }: { service: Service; opt: ServicePayment }) {
  // Layout reserves a language selector slot for future JS/TS additions —
  // Python is the only currently-supported snippet.
  const [lang] = useState<"python">("python");

  // Prefer the resolved asset symbol from the selected payment option. The
  // previous network-based heuristic mislabeled OBOL on base-sepolia as USDC
  // and any non-mainnet USDC deployment as OBOL.
  const tokenName =
    opt.asset?.symbol ?? (opt.network === "ethereum" ? "OBOL" : "USDC");
  const python = `import httpx
from x402.client import x402_client

client = x402_client(
    httpx.Client(),
    private_key="<your-${tokenName}-holder-private-key>",
)
resp = client.get("${service.endpoint}")
print(resp.json())`;

  return (
    <div>
      <div className="flex gap-1 mb-2 text-xs">
        <span className="px-2 py-1 rounded bg-bg03 text-obol-green border border-obol-green/30">
          Python
        </span>
        <span className="px-2 py-1 rounded text-text-muted" title="Coming soon">
          JS/TS (soon)
        </span>
      </div>
      {lang === "python" && <Snippet code={python} />}
    </div>
  );
}

function Snippet({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);

  const copy = () => {
    navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="relative">
      <pre className="rounded bg-bg01 p-3 pr-12 text-xs font-mono text-text-light overflow-x-auto border border-stroke whitespace-pre-wrap">
        {code}
      </pre>
      <button
        onClick={copy}
        className={`absolute top-2 right-2 rounded-md border bg-bg03 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wider transition-colors cursor-pointer ${
          copied
            ? "border-obol-green text-obol-green"
            : "border-stroke text-text-body hover:border-obol-green hover:text-obol-green"
        }`}
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}
