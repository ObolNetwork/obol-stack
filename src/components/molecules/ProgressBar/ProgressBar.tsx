import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

export interface ProgressBarProps {
  value: number; // 0-100
  label?: string;
  valueText?: string;
  variant?: 'success' | 'warning' | 'error';
  showPercentage?: boolean;
  compact?: boolean;
}

const ProgressContainer = styled(Box, {
  width: '100%',

  variants: {
    compact: {
      false: {},
      true: {},
    },
  },
});

const CompactContainer = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$xs',
});

const LabelContainer = styled(Flex, {
  justifyContent: 'space-between',
  alignItems: 'center',
  marginBottom: '$xxs',

  variants: {
    compact: {
      false: {
        marginBottom: '$xs',
      },
      true: {
        marginBottom: '$xxs',
      },
    },
  },
});

const Label = styled(Text, {
  fontSize: '$2',
  letterSpacing: '$wide',
  color: '$muted',
  textTransform: 'uppercase',
});

const ValueText = styled(Text, {
  fontSize: '$3',
  color: '$light',
});

const ProgressTrack = styled(Box, {
  height: '$2',
  borderRadius: '$pill',
  backgroundColor: 'rgba(255, 255, 255, 0.1)',
  overflow: 'hidden',
});

const ProgressFill = styled(Box, {
  height: '$2',
  borderRadius: '$pill',
  transition: 'width 500ms ease',

  variants: {
    variant: {
      success: {
        backgroundColor: '$obolGreen',
      },
      warning: {
        backgroundColor: '#F59E0B', // Amber-500
      },
      error: {
        backgroundColor: '#EF4444', // Red-500
      },
    },
  },

  defaultVariants: {
    variant: 'success',
  },
});

const PercentageBadge = styled(Box, {
  borderRadius: '$pill',
  padding: '$xxxs',
  fontSize: '$2',
  backgroundColor: 'rgba(34, 197, 94, 0.1)', // Emerald-500/10
  color: '#6EE7B7', // Emerald-300
  border: '1px solid rgba(52, 211, 153, 0.2)', // Emerald-400/20
  marginLeft: 'auto',
});

const PercentageText = styled(Text, {
  fontSize: '$2',
  color: '$muted',
  marginTop: '$1',
  textAlign: 'right',
});

const FlexOne = styled(Box, {
  flex: 1,
});

export const ProgressBar: React.FC<ProgressBarProps> = ({
  value,
  label,
  valueText,
  variant,
  showPercentage = false,
  compact = false,
}) => {
  // Auto-determine variant based on value if not provided
  const getVariant = (val: number): 'success' | 'warning' | 'error' => {
    if (variant) return variant;
    if (val >= 90) return 'error';
    if (val >= 75) return 'warning';
    return 'success';
  };

  const currentVariant = getVariant(value);
  const clampedValue = Math.min(Math.max(value, 0), 100);

  if (compact) {
    return (
      <CompactContainer>
        <FlexOne>
          {label && (
            <LabelContainer compact={true}>
              <Label>{label}</Label>
              {valueText && <ValueText>{valueText}</ValueText>}
            </LabelContainer>
          )}
          <ProgressTrack>
            <ProgressFill
              variant={currentVariant}
              css={{ width: `${clampedValue}%` }}
            />
          </ProgressTrack>
        </FlexOne>
        {showPercentage && (
          <PercentageBadge as="span">{Math.round(value)}%</PercentageBadge>
        )}
      </CompactContainer>
    );
  }

  return (
    <ProgressContainer>
      {label && (
        <LabelContainer compact={false}>
          <Label>{label}</Label>
          {valueText && <ValueText>{valueText}</ValueText>}
        </LabelContainer>
      )}
      <ProgressTrack>
        <ProgressFill
          variant={currentVariant}
          css={{ width: `${clampedValue}%` }}
        />
      </ProgressTrack>
      {showPercentage && <PercentageText>{value.toFixed(1)}%</PercentageText>}
    </ProgressContainer>
  );
};
