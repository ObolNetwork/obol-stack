'use client';

import { memo, type KeyboardEvent, type MouseEvent } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { ExternalLinkIcon } from '@radix-ui/react-icons';

import { Badge } from '@/components/atoms/Badge';
import { Button } from '@/components/atoms/Button';
import { ChainIcon } from '@/components/atoms/ChainIcon';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import { tones } from '@/lib/ui/tones';
import type { ServiceListItem } from '@/lib/marketplace/types';

import { Identicon } from './Identicon';
import {
  BADGE_LABEL,
  BADGE_VARIANT,
  PRICE_UNIT_LABEL,
  STOREFRONT_TEXT_CSS,
  chainLabel,
  chainName,
  skillMdUrl,
  statusLabel,
  statusTone,
  storefrontHref,
} from './service-display';

export interface MarketplaceCardProps {
  item: ServiceListItem;
  onDetails?: (item: ServiceListItem) => void;
  /**
   * Root URL of the operator's public storefront (tunnel or local controller).
   * The footer action opens it in a new tab; ERC-8004 listings fall back to
   * the public 8004scan explorer instead.
   */
  storefrontUrl?: string;
}

// Cards view follows the Services.html "clean" card: a single calm column
// (no split price rail) with the whole card clickable through to the detail
// page. Identity + badges on top, clamped description, skill tags, and a
// footer that carries price, chain, skill.md, and the primary action.
const Card = styled('article', {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  padding: '$sm $md',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  minHeight: 208,
  cursor: 'pointer',
  overflow: 'hidden',
  contentVisibility: 'auto',
  containIntrinsicSize: '0 208px',
  transition: 'border-color 200ms ease',

  '&:hover': {
    borderColor: '$body',
  },
});

const TopRow = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: 10,
  minWidth: 0,
});

const TitleStack = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  minWidth: 0,
  flex: 1,
});

const TitleRow = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: '$xxs',
  minWidth: 0,
  maxWidth: '100%',
  overflow: 'hidden',
});

const Title = styled(Text, {
  flex: '0 1 auto',
  minWidth: 0,
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
  fontFamily: 'monospace',
  letterSpacing: 0,
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

const TypeBadgeSlot = styled('span', {
  display: 'inline-flex',
  flex: '0 0 auto',
  overflow: 'hidden',
});

const Sub = styled(Text, {
  fontSize: '$1',
  color: '$body',
  marginTop: 3,
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

// Quiet status — a solid dot in the tone colour plus a soft label. Replaces
// the filled pill so the card reads calmer, per the readability pass.
const StatusWrap = styled('span', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 6,
  flexShrink: 0,
});

const StatusDot = styled('span', {
  width: 8,
  height: 8,
  borderRadius: 999,
  flexShrink: 0,

  variants: {
    tone: {
      live: { background: tones.success.text },
      pending: { background: tones.warning.text },
      degraded: { background: tones.danger.text },
    },
  },
});

const StatusLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
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
  minHeight: 36,
});

const TagRow = styled(Flex, {
  flexWrap: 'wrap',
  gap: 6,
});

const Tag = styled(Text, {
  padding: '2px 8px',
  fontSize: '$1',
  lineHeight: 1.5,
  color: '$body',
  background: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$2',
  fontFamily: 'monospace',
  whiteSpace: 'nowrap',
});

const Footer = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  marginTop: 'auto',
  paddingTop: '$sm',
  borderTop: '1px solid $stroke',
});

const PriceBlock = styled(Box, {
  minWidth: 0,
});

const PriceAmount = styled(ObolAmountSwitch, {
  '& [data-part="amount"], & [data-part="converted-amount"]': {
    color: '$light',
    fontSize: '$5',
    fontWeight: '$semibold',
    letterSpacing: 0,
  },
  '& [data-part="symbol"]': {
    fontSize: '$2',
    color: '$body',
    marginLeft: 4,
  },
});

const PriceUnit = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  marginTop: 1,
});

const ChainBadge = styled(Flex, {
  alignItems: 'center',
  gap: 6,
  flexShrink: 0,
  width: 'fit-content',
  padding: '3px 8px 3px 5px',
  borderRadius: '$3',
  background: '$bg02',
  border: '1px solid $stroke',
  color: '$light',
  fontSize: '$1',
});

const SkillLink = styled('a', {
  border: 0,
  background: 'transparent',
  color: '$body',
  fontSize: '$1',
  display: 'inline-flex',
  alignItems: 'center',
  gap: 4,
  textDecoration: 'none',
  cursor: 'pointer',
  '&:hover': { color: '$light' },
});

function registryBadge(item: ServiceListItem) {
  if (item.namespace.startsWith('erc8004:')) {
    return <Badge variant="green">ERC-8004</Badge>;
  }
  if (item.status === 'pending-registration') {
    return <Badge variant="yellow">ERC-8004 pending</Badge>;
  }
  return null;
}

function MarketplaceCardImpl({
  item,
  onDetails,
  storefrontUrl,
}: MarketplaceCardProps) {
  const seed = item.payTo || item.bannerSeed || item.slug;
  const provider = item.namespace;
  const tone = statusTone(item.status);
  const chain = chainName(item);
  const skillUrl = skillMdUrl(item.endpoint);
  const visibleTags = item.skills.slice(0, 4);
  const badge = registryBadge(item);
  // The whole card opens the in-app detail page; the footer CTA opens the
  // listing on the public storefront (8004scan for remote ERC-8004 agents).
  const storeHref = storefrontHref(item, storefrontUrl);

  return (
    <Card
      role="button"
      tabIndex={0}
      onClick={() => onDetails?.(item)}
      onKeyDown={(e: KeyboardEvent<HTMLElement>) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onDetails?.(item);
        }
      }}
    >
      <TopRow>
        <Identicon seed={seed} size={36} rounded={8} />
        <TitleStack>
          <TitleRow>
            <Title title={item.name}>{item.name}</Title>
            <TypeBadgeSlot>
              <Badge variant={BADGE_VARIANT[item.type]}>
                {BADGE_LABEL[item.type]}
              </Badge>
            </TypeBadgeSlot>
            {badge}
          </TitleRow>
          <Sub>{provider}</Sub>
        </TitleStack>
        <StatusWrap>
          <StatusDot tone={tone} />
          <StatusLabel>{statusLabel(item.status)}</StatusLabel>
        </StatusWrap>
      </TopRow>

      <Description>
        {item.description || 'x402 payment-gated service'}
      </Description>

      {visibleTags.length > 0 ? (
        <TagRow>
          {visibleTags.map(skill => (
            <Tag key={skill}>{skill}</Tag>
          ))}
          {item.skills.length > visibleTags.length ? (
            <Tag>+{item.skills.length - visibleTags.length}</Tag>
          ) : null}
        </TagRow>
      ) : null}

      <Footer>
        <PriceBlock>
          <PriceAmount
            amount={item.price || '—'}
            symbol={item.asset.symbol}
            variant="inline"
          />
          {item.price ? (
            <PriceUnit>{PRICE_UNIT_LABEL[item.priceUnit]}</PriceUnit>
          ) : null}
        </PriceBlock>

        <ChainBadge>
          {chain ? <ChainIcon chain={chain} size={14} /> : null}
          {chainLabel(item)}
        </ChainBadge>

        <Box css={{ flex: 1 }} />

        {skillUrl ? (
          <SkillLink
            href={skillUrl}
            target="_blank"
            rel="noopener noreferrer"
            onClick={(e: MouseEvent<HTMLAnchorElement>) => e.stopPropagation()}
          >
            skill.md <ExternalLinkIcon width={12} height={12} />
          </SkillLink>
        ) : null}

        {storeHref ? (
          <Button
            as="a"
            variant="primary"
            size="sm"
            css={STOREFRONT_TEXT_CSS}
            href={storeHref}
            target="_blank"
            rel="noopener noreferrer"
            onClick={(e: MouseEvent<HTMLAnchorElement>) => e.stopPropagation()}
          >
            Storefront <ExternalLinkIcon width={12} height={12} />
          </Button>
        ) : null}
      </Footer>
    </Card>
  );
}

export const MarketplaceCard = memo(MarketplaceCardImpl);
