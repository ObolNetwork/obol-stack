import type { AgentInstance } from '@/types/agent';
import type { ServiceOffer, ServiceOfferAgentLink } from '@/types/sell';

/**
 * Match a ServiceOffer to the agent that owns it via spec.upstream.namespace.
 * Returns null when the upstream namespace doesn't correspond to a live agent
 * (e.g. http services in `default` or `llm` that aren't tied to an agent).
 */
export function linkOfferToAgent(
  offer: ServiceOffer,
  agents: AgentInstance[]
): ServiceOfferAgentLink | null {
  const upstreamNs = offer.upstream?.namespace;
  if (!upstreamNs) return null;

  const match = agents.find(a => a.namespace === upstreamNs);
  if (!match) return null;

  return {
    runtime: match.runtime,
    id: match.id,
    namespace: match.namespace,
  };
}
