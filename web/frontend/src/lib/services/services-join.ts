import type { AgentInstance } from '@/types/agent';
import type {
  ServiceOffer,
  ServiceOfferAgentLink,
  ServiceOfferAgentRef,
} from '@/types/sell';

/**
 * Match a ServiceOffer to the agent that owns it. Agent-backed offers use
 * spec.agent.ref as the authoritative pointer; older HTTP/inference offers
 * fall back to spec.upstream.namespace.
 */
export function linkOfferToAgent(
  offer: ServiceOffer,
  agents: AgentInstance[]
): ServiceOfferAgentLink | null {
  if (offer.type === 'agent') {
    const match = findAgentByRef(offer.agent?.ref, agents);
    if (match) return agentLink(match);
  }

  const upstreamNs = offer.upstream?.namespace;
  if (!upstreamNs) return null;

  const match = agents.find(a => a.namespace === upstreamNs);
  if (!match) return null;

  return agentLink(match);
}

function findAgentByRef(
  ref: ServiceOfferAgentRef | undefined,
  agents: AgentInstance[]
): AgentInstance | null {
  const namespace = ref?.namespace?.trim();
  const name = ref?.name?.trim();

  if (namespace && name) {
    const exact = agents.find(a => a.namespace === namespace && a.id === name);
    if (exact) return exact;
  }

  if (namespace) {
    const byNamespace = agents.find(a => a.namespace === namespace);
    if (byNamespace) return byNamespace;
  }

  return null;
}

function agentLink(match: AgentInstance): ServiceOfferAgentLink {
  return {
    runtime: match.runtime,
    id: match.id,
    namespace: match.namespace,
  };
}
