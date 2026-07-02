'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

/**
 * Shared chrome for the My Listings / My Purchases pages: section cards,
 * header row, the wallet/earnings strip grid, and the table scaffolding.
 * Both pages used to carry byte-identical copies of these primitives.
 *
 * Grid columns differ per table, so TableHeaderRow / RowBody take
 * `gridTemplateColumns` via the `css` prop at the call site.
 */

export const Card = styled(Box, {
  background: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$2',
  padding: '$md',
});

export const Mono = styled('span', { fontFamily: 'monospace' });

export const FieldLabel = styled('div', {
  fontSize: '$1',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$muted',
  fontWeight: '$semibold',
});

export const SectionHeader = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  marginBottom: '$md',
});

export const SectionTitle = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
});

export const CountChip = styled('span', {
  background: '$bg03',
  border: '1px solid $stroke',
  padding: '2px 8px',
  borderRadius: '$pill',
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
});

// ── Wallet / earnings strip ────────────────────────────────────────────

export const StripGrid = styled('div', {
  display: 'grid',
  gridTemplateColumns: 'minmax(220px, 1.1fr) repeat(3, 1fr) auto',
  alignItems: 'stretch',
});

export const StripCell = styled(Box, {
  padding: '0 $md',
  display: 'flex',
  flexDirection: 'column',
  gap: 8,
  borderRight: '1px solid $stroke',

  variants: {
    last: { true: { borderRight: 'none' } },
    first: { true: { paddingLeft: 0, paddingRight: '$md' } },
  },
});

// ── Table scaffolding ──────────────────────────────────────────────────

export const TableHeaderRow = styled('div', {
  display: 'grid',
  gap: '$sm',
  padding: '0 $md 8px',
  fontSize: '$1',
  color: '$body',
  letterSpacing: '0.04em',
  textTransform: 'uppercase',
  fontWeight: '$semibold',
});

export const RowBody = styled('div', {
  padding: '$sm $md',
  display: 'grid',
  gap: '$sm',
  alignItems: 'center',
});
