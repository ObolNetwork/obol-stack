/**
 * Local Stitches theme extension for the operator dashboard.
 *
 * obol-ui owns the design tokens. Everything here is either
 *   - an alias onto an obol-ui token (so `$stroke` etc. keep working without
 *     inventing values), or
 *   - a raw colour with no obol-ui equivalent, centralised in `palette` so it
 *     lives in exactly one place instead of being hand-typed per component.
 *
 * Prefer `$token` strings in Stitches `css`; reach for `palette` only where a
 * raw value is required (SVG `fill`, third-party theming, rgba washes) or no
 * token exists. Do not add tokens to obol-ui from here.
 */
import { globalCss, theme } from '@obolnetwork/obol-ui';

const c = theme.colors;

/** Raw colours with no obol-ui token (or needed as literal values). */
export const palette = {
  /** Obol wordmark letterforms (brand asset colour). */
  wordmark: '#E1E9EB',
  /** Obol wordmark accent bar — same as $obolGreen. */
  wordmarkAccent: c.obolGreen.value as string,
  /** Dark ink used on top of $obolGreen fills (buttons, active chips). */
  inkOnGreen: '#062018',
  /** Base-network brand cyan wash (no matching tone). */
  cyanWash: 'rgba(60, 210, 221, 0.10)',
  /** Chain brand accents used by ChainChip. */
  chainLime: '#D4FF28',
  chainLavender: '#C4A8F5',
  chainBlue: '#5B9EFF',
  /** Sub-agent / ERC-8004 lavender accents. */
  lavender: '#B49BF0',
  periwinkle: '#8FA3F0',
  /** Bright amber for unreachable / degraded warnings and sale badges. */
  amberBright: '#F4B400',
  gold: '#F5C542',
  obolGoldSoft: '#FABA5A',
  /** Agent chat: assistant bubble surface. */
  chatBubble: '#222829',
  /** Identicon background tile. */
  identiconBg: '#0A1416',
} as const;

/** Visible keyboard focus indicator, shared by every interactive control. */
export const focusRing = {
  outline: '2px solid $obolGreen',
  outlineOffset: '2px',
} as const;

/** Spread into a Stitches style object to get a visible `:focus-visible`. */
export const focusVisible = {
  '&:focus-visible': focusRing,
} as const;

/**
 * Focus treatment for bordered text inputs: the border turns green and
 * thickens to 2px (via an inset-free 1px shadow) instead of an outline that
 * would double up with the border.
 */
export const inputFocus = {
  '&:focus': { outline: 'none' },
  '&:focus-visible': {
    outline: 'none',
    borderColor: '$obolGreen',
    boxShadow: '0 0 0 1px $obolGreen',
  },
} as const;

/**
 * App-wide globals. Token aliases are declared on :root (not via a theme
 * class) so Radix portals rendered under <body> resolve them too.
 */
export const globalStyles = globalCss({
  ':root': {
    // obol-ui has no `stroke` token, yet borders across the app use
    // `1px solid $stroke`. Alias it to obol-ui's $bg04 border tone.
    '--colors-stroke': 'var(--colors-bg04)',
    // Severe end of the PercentageChip / ProgressBar scale; obol-ui's
    // nearest red ($test) is already used for the step below it.
    '--colors-red': '#CC3333',
  },
  'html, body': {
    padding: 0,
    margin: 0,
  },
  body: {
    backgroundColor: '$bg01',
    color: '$light',
    fontFamily:
      '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Oxygen, Ubuntu, Cantarell, "Fira Sans", "Droid Sans", "Helvetica Neue", sans-serif',
  },
  a: {
    color: 'inherit',
    textDecoration: 'none',
  },
  '*': {
    boxSizing: 'border-box',
  },
  // Default focus ring for anything that doesn't style its own.
  ':focus-visible': focusRing,
});
