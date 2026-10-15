'use client';

import { styled } from '@obolnetwork/obol-ui';
import { tones } from '@/lib/ui/tones';

/**
 * Styled shell for NewListingDialog: backdrop scrim, panel, header/body/footer
 * chrome and the info/error/success banner. Pure presentation — all state
 * stays in the dialog.
 */

// Legibility scrim, not a status tint — intentionally hardcoded.
export const Backdrop = styled('div', {
  position: 'fixed',
  inset: 0,
  background: 'rgba(4, 8, 9, 0.55)',
  backdropFilter: 'blur(2px)',
  zIndex: 80,
  display: 'flex',
  alignItems: 'flex-start',
  justifyContent: 'center',
  padding: '48px 24px 24px',
  overflowY: 'auto',
});

export const Panel = styled('div', {
  width: 720,
  maxWidth: 'calc(100vw - 48px)',
  maxHeight: '92vh',
  background: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$4',
  boxShadow: '0 30px 90px rgba(0,0,0,0.55)',
  overflow: 'hidden',
  display: 'flex',
  flexDirection: 'column',
});

export const Header = styled('header', {
  padding: '$md $lg',
  display: 'flex',
  alignItems: 'center',
  gap: '$xs',
  borderBottom: '1px solid $stroke',
});

export const HeaderIcon = styled('div', {
  width: 32,
  height: 32,
  borderRadius: 8,
  background: '$obolMidGreen',
  color: '$obolGreen',
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,
});

export const Eyebrow = styled('div', {
  fontSize: '$1',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$muted',
  fontWeight: '$semibold',
  marginBottom: 2,
});

export const HeaderTitle = styled('div', {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
  letterSpacing: '-0.005em',
});

export const Body = styled('div', {
  padding: '$md $lg',
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
  overflowY: 'auto',
});

export const Footer = styled('footer', {
  padding: '$xs $lg',
  borderTop: '1px solid $stroke',
  display: 'flex',
  alignItems: 'center',
  gap: 10,
  background: '$bg02',
});

export const Banner = styled('div', {
  padding: '$xs $sm',
  borderRadius: '$2',
  fontSize: '$2',
  lineHeight: 1.4,

  variants: {
    tone: {
      info: {
        background: '$bg03',
        color: '$body',
        border: '1px solid $stroke',
      },
      error: {
        background: tones.danger.bg,
        color: '$light',
        border: `1px solid ${tones.danger.border}`,
      },
      success: {
        background: tones.success.bg,
        color: tones.success.text,
        border: `1px solid ${tones.success.border}`,
      },
    },
  },
});
