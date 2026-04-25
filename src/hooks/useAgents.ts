'use client';

import { useState, useEffect, useCallback } from 'react';
import type {
  AgentInstance,
  AgentInstanceHealth,
  AgentRuntime,
} from '@/types/agent';
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

export function useAgentToken(instance: AgentInstance | null) {
  const [token, setToken] = useState<string | null>(null);
  const [tokenError, setTokenError] = useState<string | null>(null);

  const runtime = instance?.runtime;
  const id = instance?.id;

  const fetchToken = useCallback(async () => {
    if (!runtime || !id) {
      setToken(null);
      setTokenError(null);
      return;
    }

    try {
      const response = await fetch(
        `/api/agents/token?runtime=${runtime}&id=${id}`
      );
      if (response.ok) {
        const data = await response.json();
        setToken(data?.token ?? null);
        setTokenError(null);
      } else if (response.status === 404) {
        setToken(null);
        setTokenError('Token not found');
      } else {
        setToken(null);
        let message = 'Failed to fetch token';
        try {
          const bodyText = await response.text();
          message += bodyText
            ? `: ${response.status} ${bodyText}`
            : `: ${response.status}`;
        } catch {
          // Ignore response body parsing failures.
        }
        setTokenError(message);
      }
    } catch {
      setToken(null);
      setTokenError('Failed to fetch token');
    }
  }, [id, runtime]);

  useEffect(() => {
    fetchToken();
  }, [fetchToken]);

  return { token, tokenError };
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

export function isSupportedAgentRuntime(
  runtime: string | null
): runtime is AgentRuntime {
  return runtime === 'hermes' || runtime === 'openclaw';
}
