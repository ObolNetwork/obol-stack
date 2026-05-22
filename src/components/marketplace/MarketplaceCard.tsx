'use client';

import { memo } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { ArrowRightIcon, ExternalLinkIcon } from '@radix-ui/react-icons';

import { Badge } from '@/components/atoms/Badge';
import { ChainIcon } from '@/components/atoms/ChainIcon';
import { CopyButton } from '@/components/atoms/CopyButton';
import type { ServiceListItem } from '@/lib/marketplace/types';
import type { ChainName } from '@/types/dashboard';
import type { ServiceOfferType } from '@/types/sell';

import { Identicon, shortAddress } from './Identicon';

export interface MarketplaceCardProps {
  item: ServiceListItem;
  onDetails?: (item: ServiceListItem) => void;
  onBuy?: (item: ServiceListItem) => void;
}

// Card view follows the Services.html grid card: content/action area on the
// left, price + chain rail on the right. The view is still named "cards" in
// the product; this is only the card component shape.
const Card = styled('article', {
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1fr) 188px',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  minHeight: 248,
  overflow: 'hidden',
  contentVisibility: 'auto',
  containIntrinsicSize: '0 248px',
  transition: 'border-color 200ms ease',

  '&:hover': {
    borderColor: '$bg05',
  },

  '@media (max-width: 620px)': {
    gridTemplateColumns: '1fr',
  },
});

const Content = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  minWidth: 0,
  padding: '$sm $md',
});

const TopRow = styled('div', {
  display: 'grid',
  gridTemplateColumns: '38px minmax(0, 1fr) auto',
  alignItems: 'start',
  columnGap: 12,
  minWidth: 0,
});

const TitleStack = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  minWidth: 0,
});

const TitleRow = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: '$xxs',
  minWidth: 0,
});

const Title = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
  fontFamily: 'monospace',
  letterSpacing: 0,
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

const SubRow = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: 6,
  marginTop: 4,
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

const StatusPillBox = styled('div', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxs',
  flexShrink: 0,
  width: 'fit-content',
  padding: '4px 12px 4px 8px',
  borderRadius: '$pill',
  fontSize: '$1',
  fontWeight: '$medium',
  border: '1px solid transparent',

  variants: {
    tone: {
      live: {
        background: 'rgba(47, 228, 171, 0.13)',
        borderColor: 'rgba(47, 228, 171, 0.22)',
        color: '$obolGreen',
      },
      pending: {
        background: 'rgba(250, 186, 90, 0.13)',
        borderColor: 'rgba(250, 186, 90, 0.24)',
        color: '$obolGold',
      },
      degraded: {
        background: 'rgba(221, 96, 60, 0.13)',
        borderColor: 'rgba(221, 96, 60, 0.24)',
        color: '$test',
      },
    },
  },
});

const Dotty = styled('span', {
  width: 8,
  height: 8,
  borderRadius: 999,
  background: 'currentColor',
});

const Description = styled(Text, {
  margin: 0,
  fontSize: '$3',
  color: '$body',
  lineHeight: 1.55,
  display: '-webkit-box',
  WebkitLineClamp: 2,
  WebkitBoxOrient: 'vertical',
  overflow: 'hidden',
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

const ActionRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  marginTop: 'auto',
});

const PrimaryBtn = styled('button', {
  height: 30,
  padding: '0 $xs',
  borderRadius: '$pill',
  border: 'none',
  background: '$obolGreen',
  color: '$obolDarkDark',
  fontSize: '$1',
  fontWeight: '$semibold',
  cursor: 'pointer',
  display: 'inline-flex',
  alignItems: 'center',
  gap: 5,
  transition: 'background 120ms',
  '&:hover': { background: '$obolGreenHover' },
});

const SecondaryBtn = styled('button', {
  height: 30,
  padding: '0 $xs',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  background: '$bg03',
  color: '$light',
  fontSize: '$1',
  cursor: 'pointer',
  transition: 'background 120ms, border-color 120ms',
  '&:hover': { background: '$bg04', borderColor: '$bg05' },
});

const SkillLink = styled('a', {
  border: 0,
  background: 'transparent',
  color: '$body',
  fontSize: '$2',
  display: 'inline-flex',
  alignItems: 'center',
  gap: 4,
  textDecoration: 'none',
  cursor: 'pointer',
  '&:hover': { color: '$light' },
});

const Rail = styled('aside', {
  background:
    'linear-gradient(180deg, rgba(47,228,171,0.05), rgba(47,228,171,0))',
  borderLeft: '1px solid $stroke',
  padding: '$sm $md',
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',

  '@media (max-width: 620px)': {
    borderLeft: 0,
    borderTop: '1px solid $stroke',
  },
});

const FieldLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontWeight: '$medium',
  textTransform: 'uppercase',
  letterSpacing: '0.03em',
});

const PriceBig = styled(Text, {
  fontFamily: 'monospace',
  color: '$light',
  fontSize: '$7',
  fontWeight: '$semibold',
  letterSpacing: 0,
});

const PriceToken = styled(Text, {
  fontSize: '$2',
  color: '$body',
  marginLeft: 6,
});

const PriceUnit = styled(Text, {
  fontFamily: 'monospace',
  fontSize: '$1',
  color: '$muted',
  marginTop: 2,
});

const ChainBadge = styled(Flex, {
  alignItems: 'center',
  gap: 6,
  width: 'fit-content',
  padding: '3px 8px 3px 5px',
  borderRadius: '$3',
  background: '$bg02',
  border: '1px solid $stroke',
  color: '$light',
  fontSize: '$1',
});

const RailBottom = styled(Box, {
  marginTop: 'auto',
  display: 'flex',
  flexDirection: 'column',
  gap: 4,
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
  request: 'per request',
  'agent-turn': 'per turn',
  mtok: 'per Mtok',
  hour: 'per hour',
  epoch: 'per epoch',
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

function chainName(item: ServiceListItem): ChainName | undefined {
  switch (item.network.caip2) {
    case 'eip155:1':
      return 'mainnet';
    case 'eip155:11155111':
      return 'sepolia';
    case 'eip155:8453':
      return 'base';
    case 'eip155:84532':
      return 'base-sepolia';
    default:
      return undefined;
  }
}

function chainLabel(item: ServiceListItem): string {
  switch (item.network.caip2) {
    case 'eip155:1':
      return 'Ethereum';
    case 'eip155:11155111':
      return 'Sepolia';
    case 'eip155:8453':
      return 'Base';
    case 'eip155:84532':
      return 'Base Sepolia';
    default:
      return item.network.alias || item.network.caip2;
  }
}

function skillMdUrl(endpoint: string): string | undefined {
  try {
    const u = new URL(endpoint);
    u.pathname = '/skill.md';
    u.search = '';
    u.hash = '';
    return u.toString();
  } catch {
    return undefined;
  }
}

function registryBadge(item: ServiceListItem) {
  if (item.namespace.startsWith('erc8004:')) {
    return <Badge variant="green">ERC-8004</Badge>;
  }
  if (item.status === 'pending-registration') {
    return <Badge variant="yellow">ERC-8004 pending</Badge>;
  }
  return null;
}

function MarketplaceCardImpl({ item, onDetails, onBuy }: MarketplaceCardProps) {
  const seed = item.payTo || item.bannerSeed || item.slug;
  const provider = item.namespace;
  const tone = statusTone(item.status);
  const chain = chainName(item);
  const skillUrl = skillMdUrl(item.endpoint);
  const visibleTags = item.skills.slice(0, 4);

  return (
    <Card>
      <Content>
        <TopRow>
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
          <StatusPillBox tone={tone}>
            <Dotty />
            {statusLabel(item.status)}
          </StatusPillBox>
        </TopRow>

        <Description>
          {item.description || 'x402 payment-gated service'}
        </Description>

        {visibleTags.length > 0 || registryBadge(item) ? (
          <TagRow>
            {visibleTags.map(skill => (
              <Tag key={skill}>{skill}</Tag>
            ))}
            {item.skills.length > visibleTags.length ? (
              <Tag>+{item.skills.length - visibleTags.length}</Tag>
            ) : null}
            {registryBadge(item)}
          </TagRow>
        ) : null}

        <ActionRow>
          <PrimaryBtn type="button" onClick={() => onBuy?.(item)}>
            Buy <ArrowRightIcon width={13} height={13} />
          </PrimaryBtn>
          <SecondaryBtn type="button" onClick={() => onDetails?.(item)}>
            Probe
          </SecondaryBtn>
          <Box css={{ flex: 1 }} />
          {skillUrl ? (
            <SkillLink href={skillUrl} target="_blank" rel="noreferrer">
              skill.md <ExternalLinkIcon width={12} height={12} />
            </SkillLink>
          ) : null}
        </ActionRow>
      </Content>

      <Rail>
        <Box>
          <FieldLabel>Price</FieldLabel>
          <Box css={{ marginTop: 6 }}>
            <PriceBig>{item.price || '-'}</PriceBig>
            {item.price ? <PriceToken>{item.asset.symbol}</PriceToken> : null}
            <PriceUnit>{PRICE_UNIT_LABEL[item.priceUnit]}</PriceUnit>
          </Box>
        </Box>

        <Box>
          <ChainBadge>
            {chain ? <ChainIcon chain={chain} size={14} /> : null}
            {chainLabel(item)}
          </ChainBadge>
        </Box>

        {item.modelOrRuntime ? (
          <RailBottom>
            <FieldLabel>Model / runtime</FieldLabel>
            <Mono css={{ color: '$light', fontSize: '$2' }}>
              {item.modelOrRuntime}
            </Mono>
          </RailBottom>
        ) : null}
      </Rail>
    </Card>
  );
}

export const MarketplaceCard = memo(MarketplaceCardImpl);
