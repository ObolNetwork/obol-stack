'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

import { Badge } from '@/components/atoms/Badge';
import { CopyButton } from '@/components/atoms/CopyButton';
import type { ServiceListItem } from '@/lib/marketplace/types';
import type { ServiceOfferType } from '@/types/sell';

import { Identicon, shortAddress } from './Identicon';

export interface MarketplaceCardProps {
  item: ServiceListItem;
  onDetails?: (item: ServiceListItem) => void;
  onBuy?: (item: ServiceListItem) => void;
}

const Card = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  padding: '$sm $md',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  minHeight: 240,
  transition: 'border-color 200ms ease, transform 200ms ease',

  '&:hover': {
    borderColor: '$bg05',
  },
});

const TopRow = styled(Flex, {
  alignItems: 'flex-start',
  justifyContent: 'space-between',
  gap: 12,
});

const TitleBlock = styled(Flex, {
  alignItems: 'center',
  gap: 10,
  minWidth: 0,
});

const TitleStack = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  minWidth: 0,
});

const TitleRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xxs',
  minWidth: 0,
});

const Title = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
  fontFamily: 'monospace',
  letterSpacing: '-0.005em',
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

const SubRow = styled(Flex, {
  alignItems: 'center',
  gap: 6,
  marginTop: 3,
  fontSize: '$1',
  color: '$body',
  minWidth: 0,
});

const Sub = styled(Text, {
  fontSize: '$1',
  color: '$body',
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

const Mono = styled(Text, {
  fontFamily: 'monospace',
  fontSize: '$1',
  color: '$body',
});

const Dot = styled(Text, {
  color: '$muted',
  fontSize: '$1',
});

const StatusPillBox = styled(Flex, {
  alignItems: 'center',
  gap: '$xxxs',
  flexShrink: 0,
  padding: '$xxxs $xxs',
  borderRadius: '$pill',
  fontSize: '$0',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  border: '1px solid transparent',

  variants: {
    tone: {
      live: {
        background: 'rgba(47, 228, 171, 0.14)',
        borderColor: 'rgba(47, 228, 171, 0.35)',
        color: '$obolGreen',
      },
      pending: {
        background: 'rgba(250, 186, 90, 0.14)',
        borderColor: 'rgba(250, 186, 90, 0.38)',
        color: '$obolGold',
      },
      degraded: {
        background: 'rgba(221, 96, 60, 0.14)',
        borderColor: 'rgba(221, 96, 60, 0.40)',
        color: '$test',
      },
    },
  },
});

const Dotty = styled('span', {
  width: 6,
  height: 6,
  borderRadius: 999,
  background: 'currentColor',
});

const Description = styled(Text, {
  margin: 0,
  fontSize: '$2',
  color: '$body',
  lineHeight: 1.5,
  display: '-webkit-box',
  WebkitLineClamp: 2,
  WebkitBoxOrient: 'vertical',
  overflow: 'hidden',
  flex: 1,
});

const TagRow = styled(Flex, {
  flexWrap: 'wrap',
  gap: 6,
});

const Tag = styled(Text, {
  padding: '3px 8px',
  fontSize: '$1',
  color: '$body',
  background: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$1',
  fontFamily: 'monospace',
});

const Footer = styled(Flex, {
  alignItems: 'flex-end',
  gap: '$xs',
  paddingTop: '$xs',
  borderTop: '1px solid $stroke',
});

const PriceBlock = styled(Box, {
  flex: 1,
  minWidth: 0,
  display: 'flex',
  flexDirection: 'column',
});

const PriceMain = styled(Text, {
  fontFamily: 'monospace',
  color: '$light',
  fontSize: '$5',
  fontWeight: '$medium',
  letterSpacing: '-0.01em',
});

const PriceTokenUnit = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontWeight: '$regular',
  marginLeft: '$xxxs',
});

const PriceSub = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  marginTop: 2,
  fontFamily: 'monospace',
});

const PrimaryBtn = styled('button', {
  height: 30,
  padding: '0 $xs',
  borderRadius: '$2',
  border: 'none',
  background: '$obolGreen',
  color: '$obolDarkDark',
  fontSize: '$1',
  fontWeight: '$semibold',
  cursor: 'pointer',
  transition: 'background 120ms',
  '&:hover': { background: '$obolGreenHover' },
});

const GhostBtn = styled('button', {
  height: 30,
  padding: '0 $xs',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: 'transparent',
  color: '$light',
  fontSize: '$1',
  cursor: 'pointer',
  transition: 'border-color 120ms',
  '&:hover': { borderColor: '$body' },
});

const BADGE_VARIANT: Record<ServiceOfferType, 'purple' | 'code' | 'neutral'> = {
  agent: 'purple',
  inference: 'purple',
  http: 'code',
  'fine-tuning': 'neutral',
};

const BADGE_LABEL: Record<ServiceOfferType, string> = {
  agent: 'Agent',
  inference: 'Inference',
  http: 'HTTP',
  'fine-tuning': 'Fine-tune',
};

const PRICE_UNIT_LABEL: Record<ServiceListItem['priceUnit'], string> = {
  request: '/ req',
  'agent-turn': '/ turn',
  mtok: '/ Mtok',
  hour: '/ hr',
  epoch: '/ epoch',
};

function statusTone(
  status: ServiceListItem['status']
): 'live' | 'pending' | 'degraded' {
  switch (status) {
    case 'live':
      return 'live';
    case 'pending-registration':
      return 'pending';
    case 'degraded':
    default:
      return 'degraded';
  }
}

function statusLabel(status: ServiceListItem['status']): string {
  switch (status) {
    case 'live':
      return 'Live';
    case 'pending-registration':
      return 'Pending';
    case 'degraded':
      return 'Degraded';
  }
}

export function MarketplaceCard({
  item,
  onDetails,
  onBuy,
}: MarketplaceCardProps) {
  const seed = item.payTo || item.bannerSeed || item.slug;
  const provider = item.namespace;
  const tone = statusTone(item.status);

  return (
    <Card>
      <TopRow>
        <TitleBlock>
          <Identicon seed={seed} size={38} rounded={8} />
          <TitleStack>
            <TitleRow>
              <Title title={item.name}>{item.name}</Title>
              <Badge variant={BADGE_VARIANT[item.type]}>
                {BADGE_LABEL[item.type]}
              </Badge>
            </TitleRow>
            <SubRow>
              <Sub>{provider}</Sub>
              {item.payTo ? (
                <>
                  <Dot>·</Dot>
                  <Mono>{shortAddress(item.payTo)}</Mono>
                  <CopyButton text={item.payTo} title="Copy wallet" />
                </>
              ) : null}
            </SubRow>
          </TitleStack>
        </TitleBlock>
        <StatusPillBox tone={tone}>
          <Dotty />
          {statusLabel(item.status)}
        </StatusPillBox>
      </TopRow>

      <Description>{item.description || '—'}</Description>

      {item.skills.length > 0 ? (
        <TagRow>
          {item.skills.slice(0, 4).map(skill => (
            <Tag key={skill}>{skill}</Tag>
          ))}
          {item.skills.length > 4 ? <Tag>+{item.skills.length - 4}</Tag> : null}
        </TagRow>
      ) : null}

      <Footer>
        <PriceBlock>
          <Text>
            <PriceMain>{item.price || '—'}</PriceMain>
            <PriceTokenUnit>
              {item.asset.symbol} {PRICE_UNIT_LABEL[item.priceUnit]}
            </PriceTokenUnit>
          </Text>
          <PriceSub>
            {item.network.alias || item.network.caip2}
            {item.modelOrRuntime ? ` · ${item.modelOrRuntime}` : ''}
          </PriceSub>
        </PriceBlock>
        <GhostBtn type="button" onClick={() => onDetails?.(item)}>
          Details →
        </GhostBtn>
        <PrimaryBtn type="button" onClick={() => onBuy?.(item)}>
          Buy
        </PrimaryBtn>
      </Footer>
    </Card>
  );
}
