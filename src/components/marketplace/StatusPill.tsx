'use client';

import type { ReactNode } from 'react';
import { styled } from '@obolnetwork/obol-ui';
import { tones, type ToneName } from '@/lib/ui/tones';

/**
 * Shared uppercase status chip for the My Listings / My Purchases tables.
 * Tone-driven so status colors always come from `tones` instead of the
 * per-page rgba washes both pages used to re-implement.
 *
 * MarketplaceCard keeps its own pill (different padding + status dot) — this
 * one is only for the dense table rows.
 */
const Pill = styled('span', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 6,
  padding: '2px 8px',
  borderRadius: '$pill',
  fontSize: '$1',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  border: '1px solid transparent',

  variants: {
    tone: {
      success: {
        background: tones.success.bg,
        color: tones.success.text,
        borderColor: tones.success.border,
      },
      warning: {
        background: tones.warning.bg,
        color: tones.warning.text,
        borderColor: tones.warning.border,
      },
      danger: {
        background: tones.danger.bg,
        color: tones.danger.text,
        borderColor: tones.danger.border,
      },
      neutral: {
        background: tones.neutral.bg,
        color: tones.neutral.text,
        borderColor: tones.neutral.border,
      },
    },
  },

  defaultVariants: {
    tone: 'neutral',
  },
});

export interface StatusPillProps {
  tone?: ToneName;
  label: ReactNode;
}

export function StatusPill({ tone, label }: StatusPillProps) {
  return <Pill tone={tone}>{label}</Pill>;
}
