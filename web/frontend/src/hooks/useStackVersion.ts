'use client';

import { useCallback, useEffect, useState } from 'react';
import { config } from '@/lib/config/environment';

export type StackVersionInfo = {
  obolVersion: string | null;
  frontendTag: string;
  frontendCommit: string;
  frontendBuildTime: string | null;
};

const fallback: StackVersionInfo = {
  obolVersion: null,
  frontendTag: config.tagVersion,
  frontendCommit: config.commitSha,
  frontendBuildTime: config.buildTime || null,
};

export function useStackVersion() {
  const [info, setInfo] = useState<StackVersionInfo>(fallback);

  const refetch = useCallback(async () => {
    try {
      const response = await fetch('/api/version', { cache: 'no-store' });
      if (!response.ok) {
        return;
      }
      const data = await response.json();
      setInfo({
        obolVersion:
          typeof data?.obol?.version === 'string' && data.obol.version.trim()
            ? data.obol.version.trim()
            : null,
        frontendTag:
          typeof data?.frontend?.tag === 'string' && data.frontend.tag.trim()
            ? data.frontend.tag.trim()
            : config.tagVersion,
        frontendCommit:
          typeof data?.frontend?.commit === 'string' &&
          data.frontend.commit.trim()
            ? data.frontend.commit.trim()
            : config.commitSha,
        frontendBuildTime:
          typeof data?.frontend?.buildTime === 'string' &&
          data.frontend.buildTime.trim()
            ? data.frontend.buildTime.trim()
            : config.buildTime || null,
      });
    } catch {
      // Keep baked-in frontend tag; Obol version stays unknown.
    }
  }, []);

  useEffect(() => {
    void refetch();
  }, [refetch]);

  return { ...info, refetch };
}
