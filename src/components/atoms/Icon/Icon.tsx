import React, { useState, useEffect } from 'react';
import { styled } from '@/lib/stitches.config';
import DOMPurify from 'dompurify';

// Singleton cache for SVG content to prevent re-fetching
const svgCache = new Map<string, string>();

// AUTO-GENERATED: Do not edit manually
// Generated from SVG files in /public directory
// Run 'npm run generate-icon-types' to regenerate
export type IconName =
  | 'node-cpu'
  | 'node-disk'
  | 'node-memory'
  | 'node-uptime'
  | 'peers'
  | 'stack-icon';

// Define size variants
export type IconSize = 'xxs' | 'xs' | 's' | 'm' | 'l' | 'xl' | 'xxl';

export interface IconProps {
  name: IconName;
  size?: IconSize;
  color?: string;
  className?: string;
  'aria-label'?: string;
  role?: string;
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
  role = 'img',
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

        // Development-time validation
        if (process.env.NODE_ENV === 'development') {
          console.debug(`🔍 Icon: Loading "${name}.svg"`);
        }

        // Check cache first to prevent re-fetching
        if (svgCache.has(name)) {
          setSvgContent(svgCache.get(name)!);
          setLoading(false);
          return;
        }

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

        // Cache the sanitized SVG for future use
        svgCache.set(name, sanitizedSvg);
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
      <IconContainer
        size={size}
        className={className}
        style={{ color }}
        aria-label={ariaLabel || `Loading ${name} icon`}
        role={role}
      >
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
      <IconContainer
        size={size}
        className={className}
        style={{ color }}
        aria-label={ariaLabel || `${name} icon failed to load`}
        role={role}
      >
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
      aria-label={ariaLabel || `${name} icon`}
      role={role}
      dangerouslySetInnerHTML={{ __html: svgContent }}
    />
  );
}
