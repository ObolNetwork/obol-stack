export type AgentRuntime = 'hermes';

export interface AgentInstance {
  id: string;
  runtime: AgentRuntime;
  namespace: string;
  hostname: string;
  dashboardHostname?: string;
  status: 'running' | 'pending' | 'error' | 'unknown';
  /** `obol agent new` — namespace `agent-<name>`, sellable via `obol sell agent`. */
  isSubAgent?: boolean;
  /** Joined ServiceOffer for sub-agents (from CR, not catalog-only). */
  listing?: AgentListing;
}

export type AgentListingPhase = 'setting_up' | 'live' | 'draining';

export interface AgentListing {
  offerNamespace: string;
  offerName: string;
  phase: AgentListingPhase;
  displayStatus: string;
  inCatalog: boolean;
  endpoint?: string;
  anchorId: string;
}

export interface AgentInstanceHealth {
  id: string;
  runtime: AgentRuntime;
  healthy: boolean;
  model: string;
  provider?: string;
  endpoint: string;
  podStatus?: string;
  restarts?: number;
  error?: string;
  apiConnected?: boolean;
  apiError?: string;
}

export interface AgentToken {
  token: string;
}
