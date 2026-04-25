export type AgentRuntime = 'hermes' | 'openclaw';

export interface AgentInstance {
  id: string;
  runtime: AgentRuntime;
  namespace: string;
  hostname: string;
  dashboardHostname?: string;
  status: 'running' | 'pending' | 'error' | 'unknown';
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
