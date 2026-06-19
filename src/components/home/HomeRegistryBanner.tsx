'use client';

import { Box, Text, styled } from '@obolnetwork/obol-ui';
import { useRegistryAgents } from '../../hooks/useRegistryAgents';
import { LoadingState } from '../molecules/LoadingState';

interface HomeRegistryBannerProps {
  onBrowseMarketplace?: () => void;
}

const Banner = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: '$lg',
  padding: '$md $lg',
  backgroundColor: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$4',
  boxSizing: 'border-box',
});

const AvatarStack = styled('div', {
  display: 'flex',
  alignItems: 'center',
  flexShrink: 0,
});

const AvatarCircle = styled('div', {
  width: 34,
  height: 34,
  borderRadius: '50%',
  border: '2px solid $bg02',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  fontSize: '$1',
  fontWeight: '$semibold',
  flexShrink: 0,
});

const TextBlock = styled('div', {
  flex: 1,
});

const BrowseButton = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 6,
  padding: '8px 16px',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  backgroundColor: '$bg03',
  color: '$light',
  fontSize: '$3',
  fontWeight: '$medium',
  fontFamily: 'inherit',
  cursor: 'pointer',
  transition: 'background 120ms, border-color 120ms',
  whiteSpace: 'nowrap',
  flexShrink: 0,
  '&:hover': {
    backgroundColor: '$bg04',
    borderColor: 'rgba(255,255,255,0.12)',
  },
});

const SEEDS = [
  { letter: 'A', bg: 'rgba(47, 228, 171, 0.25)', color: '#2FE4AB' },
  { letter: 'K', bg: 'rgba(60, 210, 221, 0.25)', color: '#3CD2DD' },
  { letter: 'M', bg: 'rgba(145, 103, 228, 0.25)', color: '#9167E4' },
  { letter: 'B', bg: 'rgba(250, 186, 90, 0.25)', color: '#FABA5A' },
  { letter: 'S', bg: 'rgba(182, 234, 92, 0.25)', color: '#B6EA5C' },
  { letter: 'H', bg: 'rgba(221, 96, 60, 0.25)', color: '#DD603C' },
  { letter: 'P', bg: 'rgba(47, 228, 171, 0.25)', color: '#2FE4AB' },
  { letter: 'C', bg: 'rgba(60, 210, 221, 0.25)', color: '#3CD2DD' },
];

export function HomeRegistryBanner({
  onBrowseMarketplace,
}: HomeRegistryBannerProps) {
  const { total, loading } = useRegistryAgents();
  const count = total;
  const overflow = Math.max(0, count - SEEDS.length);

  if (loading)
    return <LoadingState size="small" message="Loading registry..." />;

  return (
    <Banner>
      <AvatarStack>
        {SEEDS.map((seed, i) => (
          <AvatarCircle
            key={i}
            style={{
              marginLeft: i === 0 ? 0 : -10,
              position: 'relative',
              zIndex: SEEDS.length - i,
              backgroundColor: seed.bg,
              color: seed.color,
            }}
          >
            {seed.letter}
          </AvatarCircle>
        ))}
        <AvatarCircle
          style={{
            marginLeft: -10,
            backgroundColor: 'var(--colors-bg04, #243D42)',
            color: 'var(--colors-body, #9CC2C9)',
            fontFamily: 'monospace',
          }}
        >
          +{overflow}
        </AvatarCircle>
      </AvatarStack>

      <TextBlock>
        <Text
          css={{ fontSize: '$4', fontWeight: '$semibold', color: '$light' }}
        >
          ERC-8004 On-chain Agent Registry
        </Text>
        <Text css={{ fontSize: '$2', color: '$body', marginTop: 2 }}>
          {count > 0 ? (
            <>
              <strong style={{ color: 'var(--colors-light)' }}>{count}</strong>{' '}
              agents registered on-chain on Ethereum · live count from{' '}
              <span style={{ fontFamily: 'monospace' }}>8004scan.io</span> ·
              browse all of them below ↓
            </>
          ) : (
            'No agents found in the ERC-8004 registry on Ethereum (via 8004scan.io)'
          )}
        </Text>
      </TextBlock>

      <BrowseButton onClick={onBrowseMarketplace}>
        Marketplace
        <svg width="12" height="12" viewBox="0 0 16 16" fill="none">
          <path
            d="M3 8h10m-4-4 4 4-4 4"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </BrowseButton>
    </Banner>
  );
}
