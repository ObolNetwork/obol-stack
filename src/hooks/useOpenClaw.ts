'use client';

import { useState, useEffect, useCallback } from 'react';
import type {
  OpenClawInstance,
  OpenClawInstanceHealth,
  WalletMetadata,
} from '@/types/openclaw';

export function useOpenClawInstances() {
  const [instances, setInstances] = useState<OpenClawInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchInstances = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const response = await fetch('/api/openclaw/instances');

      if (!response.ok) {
        throw new Error('Failed to fetch instances');
      }

      const data = await response.json();
      console.log('Fetch OpenClaw Instances Response:', data);
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

export function useOpenClawStatus(id: string) {
  const [status, setStatus] = useState<OpenClawInstanceHealth | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchStatus = useCallback(async () => {
    try {
      setLoading(true);
      const response = await fetch(`/api/openclaw/status?id=${id}`);

      if (response.status === 404) {
        // Instance doesn't exist
        return null;
      }

      if (!response.ok) {
        throw new Error('Failed to fetch status');
      }

      const data = await response.json();
      setStatus(data);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    if (id) {
      fetchStatus();
    }
  }, [id, fetchStatus]);

  return { status, loading, error, refetch: fetchStatus };
}

export function useOpenClawToken(instanceId: string) {
  const [token, setToken] = useState<string | null>(null);
  const [tokenError, setTokenError] = useState<string | null>(null);

  const fetchToken = useCallback(async () => {
    try {
      const response = await fetch(`/api/openclaw/token?id=${instanceId}`);
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
          if (bodyText) {
            message += `: ${response.status} ${bodyText}`;
          } else {
            message += `: ${response.status}`;
          }
        } catch {
          // Ignore errors while reading the response body
        }
        setTokenError(message);
      }
    } catch {
      setToken(null);
      setTokenError('Failed to fetch token');
    }
  }, [instanceId]);

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

export function useWalletMetadata(instanceId: string) {
  const [metadata, setMetadata] = useState<WalletMetadata | null>(null);

  const fetchMetadata = useCallback(async () => {
    try {
      const response = await fetch(`/api/openclaw/wallet?id=${instanceId}`);
      if (response.ok) {
        const data = await response.json();
        setMetadata(data);
      }
    } catch {
      setMetadata(null);
    }
  }, [instanceId]);

  useEffect(() => {
    fetchMetadata();
  }, [fetchMetadata]);

  return { metadata };
}
