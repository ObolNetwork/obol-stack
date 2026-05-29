import { useState, useRef, useEffect, useId, type ReactNode } from 'react';
import { Box, styled, theme } from '@obolnetwork/obol-ui';
import { Up } from '@/icons';

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

const AccordionHeader = styled('button', {
  position: 'relative',
  display: 'flex',
  alignItems: 'start',
  width: '100%',
  padding: 0,
  border: 0,
  background: 'transparent',
  color: 'inherit',
  font: 'inherit',
  textAlign: 'left',
  cursor: 'pointer',
  transition: 'all 0.2s ease',

  '&:disabled': {
    cursor: 'not-allowed',
    opacity: 0.6,
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
  cursor: 'pointer',
  top: '10px',
  right: '0',
  width: '$lg',
  height: '$lg',
  transition: 'transform 0.3s ease',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  transformOrigin: 'center',
  // Up icon points up by default, so rotate 180deg when collapsed to point down
  transform: 'translateY(-50%) rotate(180deg)',

  variants: {
    expanded: {
      true: {
        transform: 'translateY(-50%) rotate(0deg)',
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
  const reactId = useId();
  const headerId = `${reactId}-accordion-header`;
  const contentId = `${reactId}-accordion-content`;

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
        // Measure then animate to that height
        const scrollHeight = contentRef.current.scrollHeight;
        contentRef.current.style.height = '0px';
        requestAnimationFrame(() => {
          if (contentRef.current) {
            contentRef.current.style.height = `${scrollHeight}px`;
          }
        });
      } else {
        // If currently 'auto' (set after open animation), pin to px first so
        // the collapse transition has a concrete start value
        if (contentRef.current.style.height === 'auto') {
          const scrollHeight = contentRef.current.scrollHeight;
          contentRef.current.style.height = `${scrollHeight}px`;
          requestAnimationFrame(() => {
            if (contentRef.current) {
              contentRef.current.style.height = '0px';
            }
          });
        } else {
          contentRef.current.style.height = '0px';
        }
      }
    }
  }, [isExpanded]);

  // After the open animation finishes, switch to 'auto' so dynamic content
  // (data loaded after mount) can grow the container freely
  const handleTransitionEnd = () => {
    if (isExpanded && contentRef.current) {
      contentRef.current.style.height = 'auto';
    }
  };

  return (
    <AccordionContainer className={className}>
      <AccordionHeader
        type="button"
        disabled={disabled}
        id={headerId}
        aria-expanded={isExpanded}
        aria-controls={contentId}
        onClick={handleToggle}
      >
        <HeaderContent>{header}</HeaderContent>
        <ChevronIcon expanded={isExpanded} data-chevron aria-hidden="true">
          <Up size="s" color={theme.colors.body.value} />
        </ChevronIcon>
      </AccordionHeader>

      <AccordionContent
        ref={contentRef}
        id={contentId}
        role="region"
        aria-labelledby={headerId}
        aria-hidden={!isExpanded}
        expanded={isExpanded}
        onTransitionEnd={handleTransitionEnd}
      >
        {children}
      </AccordionContent>
    </AccordionContainer>
  );
}
