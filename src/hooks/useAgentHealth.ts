'use client';

import { useState, useEffect } from 'react';

interface UseAgentHealthResult {
  isAvailable: boolean;
}

/**
 * Hook to check if the ADK agent is available and configured
 */
export function useAgentHealth(): UseAgentHealthResult {
  const [isAvailable, setIsAvailable] = useState(false);

  useEffect(() => {
    const checkHealth = async () => {
      try {
        const response = await fetch('/api/copilotkit/health');

        if (!response.ok) {
          throw new Error(`Health check failed: ${response.status}`);
        }

        const healthData = await response.json();
        setIsAvailable(healthData.available === true);
      } catch (err) {
        setIsAvailable(false);
        console.error('Agent health check error:', err);
      }
    };

    // Initial check
    checkHealth();
  }, []);

  return {
    isAvailable,
  };
}
