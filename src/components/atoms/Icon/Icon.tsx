import React from 'react';
import { styled } from '@/lib/stitches.config';
import { Image } from '@obolnetwork/obol-ui';

// AUTO-GENERATED: Do not edit manually
// Generated from SVG files in /public directory
// Run 'npm run generate-icon-types' to regenerate
export type IconName =
  | 'add-circle'
  | 'behind'
  | 'bottleneck'
  | 'clear'
  | 'close'
  | 'healthy'
  | 'high-usage'
  | 'low-peer'
  | 'node-cpu'
  | 'node-disk'
  | 'node-memory'
  | 'node-uptime'
  | 'peers'
  | 'stack-icon'
  | 'stalled-sync';

// Define size variants
export type IconSize = 'xxs' | 'xs' | 's' | 'm' | 'l' | 'xl' | 'xxl';

export interface IconProps {
  name: IconName;
  size?: IconSize;
  color?: string;
  className?: string;
  'aria-label'?: string;
}

// Size mapping
const sizeMap: Record<IconSize, number> = {
  xxs: 12,
  xs: 16,
  s: 24,
  m: 32,
  l: 40,
  xl: 48,
  xxl: 80,
};

const IconContainer = styled('div', {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  color: 'inherit', // Inherit color from parent

  // Ensure SVG inherits color properly
  '& svg': {
    fill: 'currentColor',
    color: 'inherit',
    display: 'block',
  },

  // Override fill colors in nested paths to use currentColor
  '& svg path': {
    fill: 'currentColor',
  },

  // Handle hover states - color will be inherited from parent's hover state
  '&:hover': {
    color: 'inherit',
  },

  variants: {
    size: {
      xxs: {
        width: `${sizeMap.xxs}px`,
        height: `${sizeMap.xxs}px`,
        '& svg': {
          width: `${sizeMap.xxs}px`,
          height: `${sizeMap.xxs}px`,
        },
      },
      xs: {
        width: `${sizeMap.xs}px`,
        height: `${sizeMap.xs}px`,
        '& svg': {
          width: `${sizeMap.xs}px`,
          height: `${sizeMap.xs}px`,
        },
      },
      s: {
        width: `${sizeMap.s}px`,
        height: `${sizeMap.s}px`,
        '& svg': {
          width: `${sizeMap.s}px`,
          height: `${sizeMap.s}px`,
        },
      },
      m: {
        width: `${sizeMap.m}px`,
        height: `${sizeMap.m}px`,
        '& svg': {
          width: `${sizeMap.m}px`,
          height: `${sizeMap.m}px`,
        },
      },
      l: {
        width: `${sizeMap.l}px`,
        height: `${sizeMap.l}px`,
        '& svg': {
          width: `${sizeMap.l}px`,
          height: `${sizeMap.l}px`,
        },
      },
      xl: {
        width: `${sizeMap.xl}px`,
        height: `${sizeMap.xl}px`,
        '& svg': {
          width: `${sizeMap.xl}px`,
          height: `${sizeMap.xl}px`,
        },
      },
      xxl: {
        width: `${sizeMap.xxl}px`,
        height: `${sizeMap.xxl}px`,
        '& svg': {
          width: `${sizeMap.xxl}px`,
          height: `${sizeMap.xxl}px`,
        },
      },
    },
  },

  defaultVariants: {
    size: 's',
  },
});

export function Icon({
  name,
  size = 's',
  color = 'currentColor',
  className,
  'aria-label': ariaLabel,
}: IconProps) {
  return (
    <IconContainer size={size} className={className} style={{ color }}>
      <Image
        src={`/${name}.svg`}
        alt={ariaLabel || `${name} icon`}
        style={{
          width: '100%',
          height: '100%',
          objectFit: 'contain',
        }}
      />
    </IconContainer>
  );
}
