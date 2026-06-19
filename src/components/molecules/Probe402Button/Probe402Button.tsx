'use client';

import { useState } from 'react';
import { Text, styled } from '@obolnetwork/obol-ui';
import type { Probe402Result } from '@/lib/marketplace/x402-probe';

const Btn = styled('button', {
  height: 28,
  padding: '0 12px',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  background: '$bg03',
  color: '$light',
  fontSize: '$1',
  cursor: 'pointer',
  whiteSpace: 'nowrap',
  '&:hover': { background: '$bg04' },
  '&:disabled': { opacity: 0.5, cursor: 'not-allowed' },
});

const Result = styled(Text, {
  fontSize: '$1',
  marginTop: 4,
  display: 'block',
  variants: {
    tone: {
      ok: { color: '$obolGreen' },
      warn: { color: '#FABA5A' },
      err: { color: '$test' },
    },
  },
});

export interface Probe402ButtonProps {
  probeUrl: string;
  model?: string;
  name?: string;
  /** Compact: no result text below button (shows in title). */
  compact?: boolean;
}

/** Send an unpaid request; a 402 response means the x402 gate is working. */
export function Probe402Button({
  probeUrl,
  model,
  name,
  compact = false,
}: Probe402ButtonProps) {
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<Probe402Result | null>(null);

  const run = async () => {
    setLoading(true);
    setResult(null);
    try {
      const res = await fetch('/api/marketplace/probe', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ url: probeUrl, model, name }),
      });
      const data = (await res.json()) as Probe402Result & { error?: string };
      setResult(data);
    } catch (e) {
      setResult({
        ok: false,
        httpStatus: 0,
        gated: false,
        summary: e instanceof Error ? e.message : 'Probe failed',
        acceptsCount: 0,
        error: 'network',
      });
    } finally {
      setLoading(false);
    }
  };

  const tone = result?.gated ? 'ok' : result?.ok ? 'warn' : 'err';
  const title = result?.summary ?? 'POST without payment — expect HTTP 402';

  return (
    <div>
      <Btn
        type="button"
        onClick={() => void run()}
        disabled={loading || !probeUrl}
        title={title}
      >
        {loading ? 'Probing…' : 'Probe 402'}
      </Btn>
      {!compact && result ? (
        <Result tone={tone}>{result.summary}</Result>
      ) : null}
    </div>
  );
}
