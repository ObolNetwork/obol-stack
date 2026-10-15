import React from 'react';
import * as TooltipPrimitive from '@radix-ui/react-tooltip';
import { styled } from '@obolnetwork/obol-ui';

export interface TooltipProps {
  children: React.ReactNode;
  content: React.ReactNode;
  side?: 'top' | 'right' | 'bottom' | 'left';
  align?: 'start' | 'center' | 'end';
  sideOffset?: number;
  alignOffset?: number;
  delayDuration?: number;
  skipDelayDuration?: number;
  disableHoverableContent?: boolean;
  className?: string;
}

const StyledTooltipProvider = styled(TooltipPrimitive.Provider, {});

const StyledTooltipRoot = styled(TooltipPrimitive.Root, {});

const StyledTooltipTrigger = styled(TooltipPrimitive.Trigger, {});

const StyledTooltipContent = styled(TooltipPrimitive.Content, {
  display: 'inline-flex',
  padding: '$xxs $xs',
  flexDirection: 'column',
  justifyContent: 'center',
  alignItems: 'center',
  borderRadius: '$3',
  background: '$white',
  color: '$bg01',
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$semibold',
  lineHeight: '$base',
  maxWidth: '250px',
  wordWrap: 'break-word',
  zIndex: 50,
  '& *': {
    color: 'inherit',
  },
  boxShadow:
    '0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06)',
});

const StyledTooltipArrow = styled(TooltipPrimitive.Arrow, {
  fill: '$white',
});

export function Tooltip({
  children,
  content,
  side = 'top',
  align = 'center',
  sideOffset = 4,
  alignOffset = 0,
  delayDuration = 700,
  skipDelayDuration = 300,
  disableHoverableContent = false,
  className,
}: TooltipProps) {
  return (
    <StyledTooltipProvider
      delayDuration={delayDuration}
      skipDelayDuration={skipDelayDuration}
      disableHoverableContent={disableHoverableContent}
    >
      <StyledTooltipRoot>
        <StyledTooltipTrigger asChild className={className}>
          {children}
        </StyledTooltipTrigger>
        <TooltipPrimitive.Portal>
          <StyledTooltipContent
            side={side}
            align={align}
            sideOffset={sideOffset}
            alignOffset={alignOffset}
            avoidCollisions={true}
            collisionBoundary={undefined}
            collisionPadding={10}
            sticky="partial"
            hideWhenDetached={false}
          >
            {content}
            <StyledTooltipArrow />
          </StyledTooltipContent>
        </TooltipPrimitive.Portal>
      </StyledTooltipRoot>
    </StyledTooltipProvider>
  );
}
