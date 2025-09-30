import React, { useState, ReactNode, useRef, useEffect } from 'react';
import { Box, Flex, styled } from '@obolnetwork/obol-ui';

export interface AccordionProps {
  children: ReactNode;
  header: ReactNode;
  defaultExpanded?: boolean;
  disabled?: boolean;
  className?: string;
  onToggle?: (isExpanded: boolean) => void;
}

const AccordionContainer = styled(Box, {
  position: 'relative',
  overflow: 'hidden',
  borderRadius: '$2',
  padding: '$lg',
});

const AccordionHeader = styled(Flex, {
  position: 'relative',
  alignItems: 'start',
  cursor: 'pointer',
  width: '100%',
  transition: 'all 0.2s ease',

  variants: {
    disabled: {
      true: {
        cursor: 'not-allowed',
        opacity: 0.6,
      },
    },
  },

  '&:hover': {
    '& [data-chevron]': {
      color: '$light',
    },
  },
});

const HeaderContent = styled(Box, {
  flex: 1,
});

const ChevronIcon = styled(Box, {
  // Absolute positioning prevents chevron from pushing content to the left
  // and maintains consistent text alignment regardless of chevron state
  position: 'absolute',
  top: '0',
  right: '0',
  width: '20px',
  height: '20px',
  transition: 'transform 0.3s ease',
  fill: '#97B2B8', // var(--Text-Body, #97B2B8)
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  transformOrigin: 'center',

  variants: {
    expanded: {
      true: {
        transform: 'rotate(180deg)',
      },
    },
  },
});

const AccordionContent = styled(Box, {
  overflow: 'hidden',
  transition: 'all 0.3s cubic-bezier(0.4, 0, 0.2, 1)',

  variants: {
    expanded: {
      true: {
        opacity: 1,
      },
      false: {
        opacity: 0,
      },
    },
  },
});

export function Accordion({
  children,
  header,
  defaultExpanded = false,
  disabled = false,
  className,
  onToggle,
}: AccordionProps) {
  const [isExpanded, setIsExpanded] = useState(defaultExpanded);
  const contentRef = useRef<HTMLDivElement>(null);

  const handleToggle = () => {
    if (disabled) return;

    const newExpanded = !isExpanded;
    setIsExpanded(newExpanded);
    onToggle?.(newExpanded);
  };

  // Update the height when expansion state changes
  useEffect(() => {
    if (contentRef.current) {
      if (isExpanded) {
        // First set height to auto to measure, then animate to that height
        contentRef.current.style.height = 'auto';
        const scrollHeight = contentRef.current.scrollHeight;
        contentRef.current.style.height = '0px';
        // Force reflow then animate to the measured height
        requestAnimationFrame(() => {
          if (contentRef.current) {
            contentRef.current.style.height = `${scrollHeight}px`;
          }
        });
      } else {
        contentRef.current.style.height = '0px';
      }
    }
  }, [isExpanded]);

  return (
    <AccordionContainer className={className}>
      <AccordionHeader onClick={handleToggle} disabled={disabled}>
        <HeaderContent>{header}</HeaderContent>
        <ChevronIcon expanded={isExpanded} data-chevron>
          <svg
            width="20"
            height="20"
            viewBox="0 0 20 20"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
          >
            <path
              d="M5 7.5L10 12.5L15 7.5"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </ChevronIcon>
      </AccordionHeader>

      <AccordionContent ref={contentRef} expanded={isExpanded}>
        {children}
      </AccordionContent>
    </AccordionContainer>
  );
}
