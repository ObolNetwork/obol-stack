/**
 * Semantic status tints — the single source for the translucent
 * background/border washes that used to be hand-rolled as ad-hoc rgba()
 * literals across ~30 components. Text colors match the obol-ui theme
 * ($obolGreen, $obolGold, $test) so tinted chips stay on-palette.
 *
 * Usage in stitches css objects:
 *   background: tones.success.bg, border: `1px solid ${tones.success.border}`
 */
export interface Tone {
  /** Foreground/text color (opaque, theme-aligned). */
  text: string;
  /** Translucent fill for chips, pills, banners. */
  bg: string;
  /** Translucent border matching the fill. */
  border: string;
}

export const tones = {
  /** Positive: live, synced, verified, success. ($obolGreen) */
  success: {
    text: '#2FE4AB',
    bg: 'rgba(47, 228, 171, 0.10)',
    border: 'rgba(47, 228, 171, 0.30)',
  },
  /** Attention: syncing, pending, drain windows. ($obolGold) */
  warning: {
    text: '#E89E30',
    bg: 'rgba(232, 158, 48, 0.12)',
    border: 'rgba(232, 158, 48, 0.35)',
  },
  /** Failure: errors, stuck states, unreachable. ($test) */
  danger: {
    text: '#DD603C',
    bg: 'rgba(221, 96, 60, 0.12)',
    border: 'rgba(221, 96, 60, 0.35)',
  },
  /** Informational/metadata: runtime chips, testnet labels. */
  neutral: {
    text: '#9CC2C9',
    bg: 'rgba(255, 255, 255, 0.05)',
    border: 'rgba(255, 255, 255, 0.10)',
  },
} as const satisfies Record<string, Tone>;

export type ToneName = keyof typeof tones;
