import { createStitches } from '@stitches/react';

export const {
  styled,
  css,
  globalCss,
  keyframes,
  getCssText,
  theme,
  createTheme,
  config,
} = createStitches({
  theme: {
    colors: {
      // Missing color tokens for percentage chips
      orange: '#E89E30',
      red: '#C33',
    },
  },
});

export type CSS = Parameters<typeof css>[0];
