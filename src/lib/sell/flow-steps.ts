import {
  buildSellAgentCli,
  buildSellHttpCli,
  buildSellInferenceCli,
  buildSellStatusCli,
  type SellAgentCliParams,
  type SellHttpCliParams,
  type SellInferenceCliParams,
} from './cli-commands';

export type SellFlowKind = 'inference' | 'http' | 'agent';

export interface SellFlowStep {
  /** Short label for the numbered list. */
  title: string;
  /** Optional extra context. */
  detail?: string;
  /** When set, render a copyable CLI block. */
  cli?: string;
}

export function inferencePostCreateSteps(
  p: SellInferenceCliParams,
  namespace = 'llm'
): SellFlowStep[] {
  const gateway = buildSellInferenceCli(p);
  return [
    {
      title: 'ServiceOffer saved',
      detail:
        'The listing intent is in the cluster. Inference also needs a host gateway.',
    },
    {
      title: 'Run on your Mac (Terminal)',
      detail:
        'Starts the gateway on :8402 and wires the cluster Service. Safe to re-run.',
      cli: gateway,
    },
    {
      title: 'Wait until Ready',
      detail: 'Usually 1–2 minutes after the gateway is up.',
      cli: buildSellStatusCli(p.name, namespace),
    },
  ];
}

export function httpPostCreateSteps(
  p: SellHttpCliParams,
  namespace: string
): SellFlowStep[] {
  return [
    {
      title: 'ServiceOffer created',
      detail:
        'The controller will publish the x402 route when upstream is healthy.',
    },
    {
      title: 'Check status',
      cli: buildSellStatusCli(p.name, namespace),
    },
  ];
}

export function agentPostCreateSteps(
  p: SellAgentCliParams,
  offerNamespace: string
): SellFlowStep[] {
  return [
    {
      title: 'ServiceOffer created',
      detail:
        'The controller resolves agent.ref → upstream → payment gate → route.',
    },
    {
      title: 'Check status',
      cli: buildSellStatusCli(p.name, offerNamespace),
    },
    {
      title: 'CLI equivalent',
      detail: 'Use if you prefer Terminal or need to recreate the offer.',
      cli: buildSellAgentCli(p),
    },
  ];
}

export function inferencePreflightSteps(): SellFlowStep[] {
  return [
    {
      title: 'Fill the form',
      detail:
        'Name, Ollama model, price, chain. Payout wallet = where USDC/OBOL lands (default: stack remote-signer).',
    },
    {
      title: 'Create listing',
      detail: 'Writes the ServiceOffer CR from the dashboard.',
    },
    {
      title: 'Run the gateway command',
      detail: 'Copy the `obol sell inference` command shown after create.',
    },
  ];
}
