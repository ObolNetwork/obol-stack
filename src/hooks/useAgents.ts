'use client';

import { useState, useEffect, useCallback } from 'react';
import type {
  AgentInstance,
  AgentInstanceHealth,
  AgentRuntime,
} from '@/types/agent';
import type { AgentRegistrationRef } from '@/lib/services/agent-registration';
import type { WalletMetadata } from '@/types/openclaw';

export function useAgentInstances() {
  const [instances, setInstances] = useState<AgentInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchInstances = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const response = await fetch('/api/agents/instances');

      if (!response.ok) {
        throw new Error('Failed to fetch instances');
      }

      const data = await response.json();
      setInstances(data.instances || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchInstances();
  }, [fetchInstances]);

  return { instances, loading, error, refetch: fetchInstances };
}

export function useAgentStatus(instance: AgentInstance | null) {
  const [status, setStatus] = useState<AgentInstanceHealth | null>(null);
  const [loading, setLoading] = useState(Boolean(instance));
  const [error, setError] = useState<string | null>(null);

  const runtime = instance?.runtime;
  const id = instance?.id;

  const fetchStatus = useCallback(async () => {
    if (!runtime || !id) {
      setStatus(null);
      setLoading(false);
      setError(null);
      return null;
    }

    try {
      setLoading(true);
      const response = await fetch(
        `/api/agents/status?runtime=${runtime}&id=${id}`
      );

      if (response.status === 404) {
        setStatus(null);
        setError(null);
        return null;
      }

      if (!response.ok) {
        throw new Error('Failed to fetch status');
      }

      const data = await response.json();
      setStatus(data);
      setError(null);
      return data;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error');
      return null;
    } finally {
      setLoading(false);
    }
  }, [id, runtime]);

  useEffect(() => {
    fetchStatus();
  }, [fetchStatus]);

  return { status, loading, error, refetch: fetchStatus };
}

export function useTunnelURL() {
  const [tunnelURL, setTunnelURL] = useState<string | null>(null);

  const fetchTunnelURL = useCallback(async () => {
    try {
      const response = await fetch('/api/tunnel');
      if (response.ok) {
        const data = await response.json();
        setTunnelURL(data?.tunnelURL ?? null);
      }
    } catch {
      setTunnelURL(null);
    }
  }, []);

  useEffect(() => {
    fetchTunnelURL();
  }, [fetchTunnelURL]);

  return { tunnelURL, refetch: fetchTunnelURL };
}

export function useAgentWalletMetadata(instance: AgentInstance | null) {
  const [metadata, setMetadata] = useState<WalletMetadata | null>(null);

  const runtime = instance?.runtime;
  const id = instance?.id;

  const fetchMetadata = useCallback(async () => {
    if (!runtime || !id) {
      setMetadata(null);
      return;
    }

    try {
      const response = await fetch(
        `/api/agents/wallet?runtime=${runtime}&id=${id}`
      );
      if (response.ok) {
        const data = await response.json();
        setMetadata(data);
      } else {
        setMetadata(null);
      }
    } catch {
      setMetadata(null);
    }
  }, [id, runtime]);

  useEffect(() => {
    fetchMetadata();
  }, [fetchMetadata]);

  return { metadata };
}

export interface AgentRegistrationService {
  name: string;
  endpoint?: string;
  version?: string;
  skills?: string[];
  domains?: string[];
  available?: boolean;
  drainEndsAt?: string;
}

export interface AgentRegistrationDoc {
  type: string;
  name: string;
  description?: string;
  image?: string;
  services: AgentRegistrationService[];
  x402Support: boolean;
  active: boolean;
  supportedTrust?: string[];
  registrations?: AgentRegistrationRef[];
  metadata?: Record<string, string>;
  provenance?: Record<string, string>;
}

export function useAgentRegistration() {
  const [registration, setRegistration] = useState<AgentRegistrationDoc | null>(
    null
  );
  const [tunnelURL, setTunnelURL] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const fetchRegistration = useCallback(async () => {
    try {
      const response = await fetch('/api/agents/registration');
      if (response.ok) {
        const data = await response.json();
        setRegistration(data?.registration ?? null);
        setTunnelURL(data?.tunnelURL ?? null);
      }
    } catch {
      setRegistration(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchRegistration();
  }, [fetchRegistration]);

  return { registration, tunnelURL, loading };
}

export function isSupportedAgentRuntime(
  runtime: string | null
): runtime is AgentRuntime {
  return runtime === 'hermes' || runtime === 'openclaw';
}
