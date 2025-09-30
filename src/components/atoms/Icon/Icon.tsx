import React, { useState, useEffect } from 'react';
import { styled } from '@/lib/stitches.config';
import DOMPurify from 'dompurify';

// Define available icon names (should match SVG filenames in public folder)
export type IconName =
  | 'peers'
  | 'node-disk'
  | 'node-memory'
  | 'node-cpu'
  | 'node-uptime'
  | 'ethereum'
  | 'aztec';

// Define size variants
export type IconSize = 'xxs' | 'xs' | 's' | 'm' | 'l' | 'xl' | 'xxl';

export interface IconProps {
  name: IconName;
  size?: IconSize;
  color?: string;
  className?: string;
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
}: IconProps) {
  const [svgContent, setSvgContent] = useState<string>('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  useEffect(() => {
    const controller = new AbortController();

    const fetchSvg = async () => {
      try {
        setLoading(true);
        setError(false);

        const response = await fetch(`/${name}.svg`, {
          signal: controller.signal,
        });

        if (!response.ok) {
          throw new Error(`Failed to load icon: ${name}.svg`);
        }

        const svgText = await response.text();

        // Sanitize SVG content to prevent XSS attacks
        const sanitizedSvg = DOMPurify.sanitize(svgText, {
          USE_PROFILES: { svg: true, svgFilters: true },
          ADD_TAGS: ['svg', 'path', 'g', 'defs', 'clipPath', 'rect'],
          ADD_ATTR: [
            'viewBox',
            'xmlns',
            'fill',
            'clip-path',
            'width',
            'height',
            'd',
            'id',
          ],
        });

        setSvgContent(sanitizedSvg);
      } catch (err) {
        // Ignore AbortError when component unmounts
        if (err instanceof Error && err.name === 'AbortError') {
          return;
        }
        console.warn(`Icon "${name}.svg" not found in public folder.`, err);
        setError(true);
      } finally {
        setLoading(false);
      }
    };

    fetchSvg();

    // Cleanup: abort fetch request if component unmounts
    return () => controller.abort();
  }, [name]);

  if (loading) {
    return (
      <IconContainer size={size} className={className} style={{ color }}>
        <div
          style={{
            width: '100%',
            height: '100%',
            backgroundColor: 'currentColor',
            opacity: 0.1,
          }}
        />
      </IconContainer>
    );
  }

  if (error || !svgContent) {
    return (
      <IconContainer size={size} className={className} style={{ color }}>
        <div
          style={{
            width: '100%',
            height: '100%',
            backgroundColor: 'currentColor',
            opacity: 0.3,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontSize: '8px',
            color: 'currentColor',
          }}
        >
          ?
        </div>
      </IconContainer>
    );
  }

  return (
    <IconContainer
      size={size}
      className={className}
      style={{ color }}
      dangerouslySetInnerHTML={{ __html: svgContent }}
    />
  );
}
