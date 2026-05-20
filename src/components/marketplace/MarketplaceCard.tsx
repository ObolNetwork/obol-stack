'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

import type { ServiceListItem } from '@/lib/marketplace/types';

export interface MarketplaceCardProps {
  item: ServiceListItem;
  onDetails?: (item: ServiceListItem) => void;
  onBuy?: (item: ServiceListItem) => void;
}

const Card = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
  overflow: 'hidden',
  transition: 'border-color 0.2s ease, transform 0.2s ease',

  '&:hover': {
    borderColor: '$body',
  },
});

const Banner = styled(Box, {
  height: 96,
  position: 'relative',
});

const TypeChip = styled(Text, {
  position: 'absolute',
  top: '$xs',
  left: '$xs',
  padding: '2px 8px',
  borderRadius: '$pill',
  background: 'rgba(9, 16, 17, 0.6)',
  color: '$light',
  fontSize: '$0',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
});

const StatusDot = styled(Box, {
  position: 'absolute',
  top: '$xs',
  right: '$xs',
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxs',
  padding: '2px 8px',
  borderRadius: '$pill',
  background: 'rgba(9, 16, 17, 0.6)',
  color: '$light',
  fontSize: '$0',
});

const Body = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  padding: '$sm $md $md',
});

const Title = styled(Text, {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '$light',
});

const Sub = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
});

const Desc = styled(Text, {
  fontSize: '$1',
  color: '$body',
  display: '-webkit-box',
  WebkitLineClamp: 2,
  WebkitBoxOrient: 'vertical',
  overflow: 'hidden',
  minHeight: 32,
});

const SkillRow = styled(Flex, {
  flexWrap: 'wrap',
  gap: '$xxs',
});

const SkillPill = styled(Text, {
  padding: '2px 6px',
  borderRadius: '$pill',
  background: '$bg03',
  border: '1px solid $stroke',
  color: '$body',
  fontSize: '$0',
});

const FooterRow = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$xs',
  marginTop: '$xs',
});

const Price = styled(Flex, {
  flexDirection: 'column',
  gap: 0,
});

const PriceMain = styled(Text, {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '$light',
});

const PriceUnit = styled(Text, {
  fontSize: '$0',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
});

const Actions = styled(Flex, {
  gap: '$xs',
});

const PrimaryBtn = styled('button', {
  height: 32,
  padding: '0 $sm',
  borderRadius: '$2',
  border: 'none',
  background: '$obolGreen',
  color: '$obolDarkDark',
  fontSize: '$1',
  fontWeight: '$semibold',
  cursor: 'pointer',
  transition: 'background 0.15s ease',

  '&:hover': {
    background: '$obolGreenHover',
  },
});

const GhostBtn = styled('button', {
  height: 32,
  padding: '0 $sm',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: 'transparent',
  color: '$light',
  fontSize: '$1',
  cursor: 'pointer',
  transition: 'border-color 0.15s ease',

  '&:hover': {
    borderColor: '$body',
  },
});

function gradientFor(seed: string): string {
  // Take two hex pairs from the seed for hue rotation. Deterministic, no
  // randomness; same slug always renders the same gradient.
  const h1 = Number.parseInt(seed.slice(0, 2), 16) * (360 / 256);
  const h2 = Number.parseInt(seed.slice(2, 4), 16) * (360 / 256);
  return `linear-gradient(135deg, hsl(${h1.toFixed(0)}, 60%, 22%), hsl(${h2.toFixed(0)}, 60%, 14%))`;
}

function statusLabel(status: ServiceListItem['status']): string {
  switch (status) {
    case 'live':
      return '● live';
    case 'pending-registration':
      return '◐ pending';
    case 'degraded':
      return '◯ degraded';
  }
}

function networkLabel(item: ServiceListItem): string {
  return item.network.alias || item.network.caip2;
}

export function MarketplaceCard({
  item,
  onDetails,
  onBuy,
}: MarketplaceCardProps) {
  const typeLabel = item.type === 'fine-tuning' ? 'fine-tune' : item.type;
  return (
    <Card>
      <Banner css={{ background: gradientFor(item.bannerSeed) }}>
        <TypeChip>{typeLabel}</TypeChip>
        <StatusDot>{statusLabel(item.status)}</StatusDot>
      </Banner>
      <Body>
        <Title>{item.name}</Title>
        <Sub>{item.modelOrRuntime || '—'}</Sub>
        {item.description ? <Desc>{item.description}</Desc> : <Desc>—</Desc>}
        {item.skills.length > 0 ? (
          <SkillRow>
            {item.skills.slice(0, 3).map(s => (
              <SkillPill key={s}>{s}</SkillPill>
            ))}
            {item.skills.length > 3 ? (
              <SkillPill>+{item.skills.length - 3}</SkillPill>
            ) : null}
          </SkillRow>
        ) : null}
        <FooterRow>
          <Price>
            <PriceMain>
              {item.price || '—'} {item.asset.symbol}
            </PriceMain>
            <PriceUnit>
              per {item.priceUnit} · {networkLabel(item)}
            </PriceUnit>
          </Price>
          <Actions>
            <GhostBtn type="button" onClick={() => onDetails?.(item)}>
              Details →
            </GhostBtn>
            <PrimaryBtn type="button" onClick={() => onBuy?.(item)}>
              Buy
            </PrimaryBtn>
          </Actions>
        </FooterRow>
      </Body>
    </Card>
  );
}
