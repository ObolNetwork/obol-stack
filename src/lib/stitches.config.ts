import { createStitches } from '@stitches/react';

export const { styled } = createStitches({
  theme: {
    colors: {
      // Missing color tokens for percentage chips
      orange: '#E89E30',
      red: '#C33',
    },
    fonts: {
      body: '"DM Sans"',
    },
  },
});
