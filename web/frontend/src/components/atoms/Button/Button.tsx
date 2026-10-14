import { styled } from '@obolnetwork/obol-ui';

/**
 * Canonical button. Replaces the ~28 per-component styled('button')
 * one-offs that each invented their own radius/height/padding.
 *
 * System:
 * - Rectangular actions use radius $2; `pill` is reserved for chip-like
 *   filter/toggle buttons so the two shapes stay meaningful.
 * - Two heights only: sm (28px) for dense rows/chips, md (36px) for
 *   standalone actions.
 * - focus-visible ring is built in — never remove it at call sites.
 */
export const Button = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  gap: '$xxs',
  fontFamily: '$body',
  fontWeight: '$semibold',
  lineHeight: 1,
  whiteSpace: 'nowrap',
  border: '1px solid transparent',
  borderRadius: '$2',
  cursor: 'pointer',
  transition:
    'background-color 150ms ease, border-color 150ms ease, color 150ms ease',

  '&:focus-visible': {
    outline: '2px solid $obolGreen',
    outlineOffset: 2,
  },
  '&:disabled': {
    opacity: 0.5,
    cursor: 'not-allowed',
  },

  variants: {
    variant: {
      /** Filled green — the one primary action in a view. */
      primary: {
        background: '$obolGreen',
        color: '$obolDarkDark',
        '&:hover:not(:disabled)': { background: '$obolGreenHover' },
      },
      /** Neutral filled — default for most actions. */
      secondary: {
        background: '$bg03',
        color: '$light',
        borderColor: '$stroke',
        '&:hover:not(:disabled)': { background: '$bg04' },
      },
      /** Outline only — tertiary actions beside a primary. */
      ghost: {
        background: 'transparent',
        color: '$body',
        borderColor: '$stroke',
        '&:hover:not(:disabled)': { color: '$light', borderColor: '$bg05' },
      },
      /** Borderless text/icon — inline affordances (copy, expand). */
      bare: {
        background: 'transparent',
        color: '$body',
        borderColor: 'transparent',
        '&:hover:not(:disabled)': { color: '$light' },
      },
    },
    size: {
      sm: {
        height: 28,
        paddingLeft: '$xs',
        paddingRight: '$xs',
        fontSize: '$1',
      },
      md: {
        height: 36,
        paddingLeft: '$sm',
        paddingRight: '$sm',
        fontSize: '$2',
      },
    },
    /** Chip shape for filter/toggle rows. Not for standalone actions. */
    pill: {
      true: { borderRadius: '$pill' },
    },
    /** Square icon-only button; pair with an aria-label. */
    iconOnly: {
      true: { paddingLeft: 0, paddingRight: 0 },
    },
  },

  compoundVariants: [
    { iconOnly: true, size: 'sm', css: { width: 28 } },
    { iconOnly: true, size: 'md', css: { width: 36 } },
  ],

  defaultVariants: {
    variant: 'secondary',
    size: 'md',
  },
});
