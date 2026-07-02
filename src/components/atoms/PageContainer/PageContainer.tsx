import { styled } from '@obolnetwork/obol-ui';

/**
 * Shared page-width constraint. Header and page bodies both render inside
 * one of these so their edges always align — previously alignment relied
 * on the header and each page coincidentally choosing the same padding.
 */
export const PageContainer = styled('div', {
  width: '100%',
  maxWidth: 1280,
  marginLeft: 'auto',
  marginRight: 'auto',

  variants: {
    /** Wide grids (marketplace browse) get extra room. */
    wide: {
      true: { maxWidth: 1440 },
    },
  },
});
