'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Tooltip } from '../../atoms/Tooltip';
import {
  readinessState,
  readinessStepsForOffer,
  readinessTooltipLines,
  type ReadinessState,
} from '@/lib/services/offer-readiness';
import type { ServiceOffer } from '@/types/sell';

const Row = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$sm',
  flexWrap: 'wrap',
  marginTop: '$sm',
  paddingTop: '$xs',
  borderTop: '1px dashed $stroke',
});

const Track = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  flexWrap: 'wrap',
  flex: '1 1 auto',
  minWidth: 0,
});

const Label = styled(Text, {
  fontSize: '$1',
  color: '$body',
  flexShrink: 0,
});

const Pill = styled('span', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxxs',
  fontSize: '$1',
  padding: '2px 6px',
  borderRadius: '4px',
  border: '1px solid transparent',
  whiteSpace: 'nowrap',

  variants: {
    state: {
      ok: {
        color: '$obolGreen',
        backgroundColor: 'rgba(47, 228, 171, 0.08)',
        borderColor: 'rgba(47, 228, 171, 0.2)',
      },
      pending: {
        color: '$obolGold',
        backgroundColor: 'rgba(245, 197, 66, 0.08)',
        borderColor: 'rgba(245, 197, 66, 0.25)',
      },
      missing: {
        color: '$body',
        opacity: 0.75,
        backgroundColor: 'rgba(255, 255, 255, 0.03)',
        borderColor: 'rgba(255, 255, 255, 0.08)',
      },
    },
  },
});

const Dot = styled(Box, {
  width: 6,
  height: 6,
  borderRadius: '$round',
  backgroundColor: 'currentColor',
});

function TooltipContent({
  title,
  summary,
  message,
}: {
  title: string;
  summary: string;
  message?: string;
}) {
  return (
    <Box css={{ textAlign: 'left', maxWidth: 220 }}>
      <Text css={{ fontWeight: '$semibold', fontSize: '$2' }}>{title}</Text>
      <Text
        css={{
          fontSize: '$2',
          fontWeight: '$normal',
          lineHeight: 1.45,
          marginTop: '$xxxs',
        }}
      >
        {summary}
      </Text>
      {message ? (
        <Text
          css={{
            fontSize: '$2',
            fontWeight: '$normal',
            lineHeight: 1.45,
            marginTop: '$xxs',
          }}
        >
          {message}
        </Text>
      ) : null}
    </Box>
  );
}

export interface ServiceReadinessRowProps {
  offer: ServiceOffer;
}

export function ServiceReadinessRow({ offer }: ServiceReadinessRowProps) {
  const steps = readinessStepsForOffer(offer);

  return (
    <Row>
      <Track>
        <Label>Readiness</Label>
        {steps.map(({ key, short, summary }) => {
          const state: ReadinessState = readinessState(offer.conditions, key);
          const tip = readinessTooltipLines(key, summary, offer.conditions);
          const nativeTitle = [tip.title, tip.summary, tip.message]
            .filter(Boolean)
            .join(' — ');
          return (
            <Tooltip
              key={key}
              content={
                <TooltipContent
                  title={tip.title}
                  summary={tip.summary}
                  message={tip.message}
                />
              }
              delayDuration={400}
              side="top"
            >
              <span style={{ display: 'inline-flex' }} title={nativeTitle}>
                <Pill state={state}>
                  <Dot />
                  {short}
                </Pill>
              </span>
            </Tooltip>
          );
        })}
      </Track>
    </Row>
  );
}
