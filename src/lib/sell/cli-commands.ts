import type {
  SellPaymentChain,
  SellPaymentToken,
} from '@/lib/services/sell-assets';
import { resolveUpstreamHealthPath } from './upstream-health.ts';

export interface SellCliParams {
  name: string;
  payTo: string;
  chain: SellPaymentChain | string;
  token: SellPaymentToken | string;
  pricePerRequest: string;
}

export interface SellInferenceCliParams extends SellCliParams {
  model: string;
}

export interface SellHttpCliParams extends SellCliParams {
  upstream: string;
  port: number | string;
  namespace?: string;
  healthPath?: string;
}

export interface SellAgentCliParams extends SellCliParams {
  agentName: string;
}

export interface AgentNewCliParams {
  name: string;
  model?: string;
  skills?: string;
  createWallet?: boolean;
}

function lines(parts: string[]): string {
  return parts.join('\n');
}

/** Full `obol sell inference` — starts host gateway + ServiceOffer (canonical). */
export function buildSellInferenceCli(p: SellInferenceCliParams): string {
  return lines([
    `obol sell inference ${p.name} \\`,
    `  --model ${p.model} \\`,
    `  --per-request ${p.pricePerRequest} \\`,
    `  --token ${p.token} \\`,
    `  --chain ${p.chain} \\`,
    `  --pay-to ${p.payTo}`,
  ]);
}

export function buildSellHttpCli(p: SellHttpCliParams): string {
  const ns = p.namespace ?? 'llm';
  const healthPath = p.healthPath ?? resolveUpstreamHealthPath(p.upstream);
  return lines([
    `obol sell http ${p.name} \\`,
    `  --upstream ${p.upstream} \\`,
    `  --port ${p.port} \\`,
    `  --namespace ${ns} \\`,
    `  --health-path ${healthPath} \\`,
    `  --per-request ${p.pricePerRequest} \\`,
    `  --token ${p.token} \\`,
    `  --chain ${p.chain} \\`,
    `  --pay-to ${p.payTo}`,
  ]);
}

/** Gate an Agent CR (`obol agent new`) with x402. */
export function buildSellAgentCli(p: SellAgentCliParams): string {
  return lines([
    `obol sell agent ${p.agentName} \\`,
    `  --per-request ${p.pricePerRequest} \\`,
    `  --token ${p.token} \\`,
    `  --chain ${p.chain} \\`,
    `  --pay-to ${p.payTo}`,
  ]);
}

export interface AgentWorkflowStep {
  label: string;
  command: string;
}

export interface AgentCreateWorkflow {
  steps: AgentWorkflowStep[];
  copyText: string;
}

/**
 * Minimal home-page workflow — flags are optional on `obol agent new` (prompted on TTY).
 * Alternatives with flags: buildAgentNewCli / buildSellAgentCli for modals.
 */
export function buildAgentCreateWorkflowCli(
  name = 'obol-agent'
): AgentCreateWorkflow {
  const steps: AgentWorkflowStep[] = [
    { label: 'Create a new Agent', command: `obol agent new ${name}` },
    { label: 'Sell the new Agent', command: `obol sell agent ${name}` },
  ];
  return {
    steps,
    copyText: steps.map(s => s.command).join('\n'),
  };
}

/** Create a sub-agent before `obol sell agent` (modals / advanced flows). */
export function buildAgentNewCli(p: AgentNewCliParams): string {
  const parts = [
    `obol agent new ${p.name} \\`,
    `  --model ${p.model?.trim() || '<model>'} \\`,
    `  --skills ${p.skills?.trim() || 'addresses,gas'}`,
  ];
  if (p.createWallet !== false) {
    parts.push('  --create-wallet');
  }
  return lines(parts);
}

export function buildSellStatusCli(name: string, namespace: string): string {
  return `obol sell status ${name} -n ${namespace}`;
}
