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
function endpointOrigin(endpoint: string): string | null {
  try {
    return new URL(endpoint).origin;
  } catch {
    return null;
  }
}

function docsRef(endpoint: string): string {
  const origin = endpointOrigin(endpoint);
  if (!origin) {
    return "See https://www.x402.org for how x402 micropayments work.";
  }
  return `Read ${origin}/skill.md for the x402 payment flow and ${origin}/openapi.json for the exact request shapes.`;
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
const AGENT_TASK_PLACEHOLDER = "Summarise the README and list the top 3 risks.";

function resolvedAgentTask(task: string): string {
  return task.trim() || AGENT_TASK_PLACEHOLDER;
}

function quoteAgentTask(task: string): string {
  return JSON.stringify(resolvedAgentTask(task));
}

function buildAgentPayAgentCommand(
  endpoint: string,
  model: string | undefined,
  agentTask: string,
): string {
  const modelId = model || "<model-id>";
  return `pay-agent ${endpoint} --model ${JSON.stringify(modelId)} --message ${quoteAgentTask(agentTask)}`;
}

function buildAgentObolPrompt(
  endpoint: string,
  model: string | undefined,
  agentTask: string,
): string {
  return `Use the buy-x402 skill's \`pay-agent\` command to buy one round of work from this Obol Agent (skills, tools, and memory — not a raw LLM):

${buildAgentPayAgentCommand(endpoint, model, agentTask)}`;
}

export function ServiceCard({ service }: { service: Service }) {
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<Tab>("agent");
  const [copied, setCopied] = useState(false);
  const [agentTask, setAgentTask] = useState("");

  const options = paymentOptions(service);
  const [optIdx, setOptIdx] = useState(0);
  const opt = options[optIdx] ?? options[0];
  const multiPay = options.length > 1;
  const kind = normalizeOfferType(service.type);
  const needsAgentTask = kind === "agent";
  const taskReady = agentTask.trim().length > 0;

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
        {kind === "http" && endpointOrigin(service.endpoint) ? (
          <div className="col-span-2">
            <span className="text-text-muted">API docs</span>
            <a
              href={`${endpointOrigin(service.endpoint)}/api`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-xs text-obol-green hover:underline"
            >
              Swagger UI ↗
            </a>
            <span className="text-text-muted text-xs mx-1">·</span>
            <a
              href={`${endpointOrigin(service.endpoint)}/openapi.json`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-xs text-obol-green hover:underline"
            >
              openapi.json ↗
            </a>
          </div>
        ) : null}
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

          {needsAgentTask && (
            <div className="space-y-1.5">
              <label
                htmlFor={`${anchorId}-task`}
                className="block text-xs font-semibold text-text-light"
              >
                What should this agent do?
              </label>
              <textarea
                id={`${anchorId}-task`}
                value={agentTask}
                onChange={(e) => setAgentTask(e.target.value)}
                rows={3}
                placeholder={AGENT_TASK_PLACEHOLDER}
                className="w-full rounded border border-stroke bg-bg01 px-3 py-2 text-sm text-text-light placeholder:text-text-muted focus:border-obol-green focus:outline-none"
              />
              <p className="text-xs text-text-muted">
                This updates the prompts below so the copied text is ready to
                use.
              </p>
            </div>
          )}

          <TabBar tab={tab} onChange={setTab} />

          {tab === "agent" && (
            <BuyViaObolAgent
              service={service}
              opt={opt}
              kind={kind}
              agentTask={agentTask}
              taskReady={taskReady}
              requireTask={needsAgentTask}
            />
          )}
          {tab === "other-ai" && (
            <BuyViaOtherAgent
              service={service}
              opt={opt}
              kind={kind}
              agentTask={agentTask}
              taskReady={taskReady}
              requireTask={needsAgentTask}
            />
          )}
          {tab === "code" && (
            <BuyWithCode
              service={service}
              opt={opt}
              kind={kind}
              agentTask={agentTask}
              taskReady={taskReady}
              requireTask={needsAgentTask}
            />
          )}
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
// `pay-agent` for agents, `obol buy inference` CLI for inference).
// Mirrors inferenceCopy/agentCopy/httpCopy in internal/x402/paymentrequired.go.
function BuyViaObolAgent({
  service,
  opt,
  kind,
  agentTask,
  taskReady,
  requireTask,
}: {
  service: Service;
  opt: ServicePayment;
  kind: "inference" | "agent" | "http";
  agentTask: string;
  taskReady: boolean;
  requireTask: boolean;
}) {

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
    const prompt = buildAgentObolPrompt(
      service.endpoint,
      service.model,
      agentTask,
    );
    return (
      <div className="space-y-2">
        <p className="text-xs text-text-muted">
          Paste this into your Obol agent. It runs{" "}
          <code className="font-mono text-obol-green">pay-agent</code> for you
          — one signed payment, one streaming response. Fill in your task above
          before copying.
        </p>
        <Snippet
          code={prompt}
          copyDisabled={requireTask && !taskReady}
          copyDisabledReason="Enter your task above first"
        />
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

function BuyViaOtherAgent({
  service,
  opt,
  kind,
  agentTask,
  taskReady,
  requireTask,
}: {
  service: Service;
  opt: ServicePayment;
  kind: "inference" | "agent" | "http";
  agentTask: string;
  taskReady: boolean;
  requireTask: boolean;
}) {

  let prompt: string;
  if (kind === "inference") {
    const model = service.model || "the advertised model";
    prompt = `${docsRef(service.endpoint)} I want to use the remote LLM at ${service.endpoint} (model ${model}) as a paid OpenAI-compatible chat-completions endpoint. Pre-sign a budget of EIP-3009 or Permit2 authorisations and POST chat-completions bodies with the X-PAYMENT header attached.`;
  } else if (kind === "agent") {
    // An agent runs its own pinned model server-side and ignores the request's
    // model field, so we don't tell the buyer which model it uses — the request
    // shape is what matters.
    prompt = `${docsRef(service.endpoint)} Help me call the Obol Agent at ${service.endpoint} — it's an autonomous agent (tools + skills + memory), not a raw LLM. POST OpenAI-style chat-completions JSON with this user message in \`messages\`: {"role":"user","content":${quoteAgentTask(agentTask)}}. Attach a signed EIP-3009 or Permit2 authorisation as \`X-PAYMENT\`, and report what the agent does.`;
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
      <Snippet
        code={prompt}
        copyDisabled={requireTask && !taskReady}
        copyDisabledReason="Enter your task above first"
      />
    </div>
  );
}

function BuyWithCode({
  service,
  opt,
  kind,
  agentTask,
  taskReady,
  requireTask,
}: {
  service: Service;
  opt: ServicePayment;
  kind: "inference" | "agent" | "http";
  agentTask: string;
  taskReady: boolean;
  requireTask: boolean;
}) {
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
${service.model ? `  "model": ${JSON.stringify(service.model)},\n` : ""}  "messages": [
    {"role": "user", "content": ${quoteAgentTask(agentTask)}}
  ]
}`}
            copyDisabled={requireTask && !taskReady}
            copyDisabledReason="Enter your task above first"
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

function Snippet({
  code,
  copyDisabled = false,
  copyDisabledReason,
}: {
  code: string;
  copyDisabled?: boolean;
  copyDisabledReason?: string;
}) {
  const [copied, setCopied] = useState(false);

  const copy = () => {
    if (copyDisabled) return;
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
        disabled={copyDisabled}
        title={copyDisabled ? copyDisabledReason : undefined}
        className={`absolute top-2 right-2 rounded-md border bg-bg03 px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wider transition-colors ${
          copyDisabled
            ? "cursor-not-allowed border-stroke text-text-muted"
            : copied
            ? "cursor-pointer border-obol-green text-obol-green"
            : "cursor-pointer border-stroke text-text-body hover:border-obol-green hover:text-obol-green"
        }`}
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}
