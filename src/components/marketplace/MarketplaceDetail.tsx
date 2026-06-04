'use client';

import { useState } from 'react';
import dynamic from 'next/dynamic';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

import { CopyButton } from '@/components/atoms/CopyButton';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import { LoadingState } from '@/components/molecules/LoadingState';
import { ErrorState } from '@/components/molecules/ErrorState';
import { StackUnreachableState } from '@/components/molecules/StackUnreachableState';
import { useServiceDetail } from '@/hooks/useServiceDetail';
import { isStackUnreachable } from '@/lib/marketplace/errors';

import type { BuyDrawerProps } from './BuyDrawer';

// Lazy-load BuyDrawer — only rendered once the user clicks "Buy access".
// (vercel-react-best-practices: bundle-dynamic-imports)
const BuyDrawer = dynamic<BuyDrawerProps>(
  () => import('./BuyDrawer.tsx').then(m => m.BuyDrawer),
  { ssr: false }
);

export interface MarketplaceDetailProps {
  slug: string;
  onBack?: () => void;
}

// Outer shell (padding/background/header) lives in
// src/app/marketplace/layout.tsx via <DashboardTemplate>. Layout here is
// just the column rhythm for the detail sections.
const Layout = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
  width: '100%',
});

const Banner = styled(Box, {
  position: 'relative',
  height: 160,
  borderRadius: '$2',
  border: '1px solid $stroke',
  overflow: 'hidden',
});

const BannerContent = styled(Flex, {
  position: 'absolute',
  inset: 0,
  alignItems: 'flex-end',
  justifyContent: 'space-between',
  padding: '$md',
  gap: '$md',
});

const HeaderBlock = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: 4,
});

const Title = styled(Text, {
  fontSize: '$7',
  fontWeight: '$semibold',
  color: '$light',
});

const SubMono = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
});

const ChipRow = styled(Flex, {
  gap: '$xxs',
  flexWrap: 'wrap',
});

const Chip = styled(Text, {
  display: 'inline-block',
  padding: '2px 8px',
  borderRadius: '$pill',
  background: 'rgba(9, 16, 17, 0.6)',
  border: '1px solid $stroke',
  color: '$light',
  fontSize: '$0',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
});

const Grid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr 320px',
  gap: '$lg',
  '@media (max-width: 960px)': { gridTemplateColumns: '1fr' },
});

const Card = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  padding: '$md',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
});

const CardTitle = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$body',
});

const Description = styled(Text, {
  fontSize: '$2',
  color: '$light',
  lineHeight: 1.6,
});

const KVRow = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$xs',
  paddingY: 4,
});

const KVLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
});

const KVValue = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
  textAlign: 'right',
  wordBreak: 'break-all',
});

const PrimaryBtn = styled('button', {
  height: 36,
  padding: '0 $md',
  borderRadius: '$2',
  border: 'none',
  background: '$obolGreen',
  color: '$obolDarkDark',
  fontSize: '$2',
  fontWeight: '$semibold',
  cursor: 'pointer',
  '&:hover': { background: '$obolGreenHover' },
});

const GhostBtn = styled('button', {
  height: 36,
  padding: '0 $md',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: 'transparent',
  color: '$light',
  fontSize: '$2',
  cursor: 'pointer',
  '&:hover': { borderColor: '$body' },
});

const ConditionTable = styled('table', {
  width: '100%',
  borderCollapse: 'collapse',
  fontFamily: '$fonts$body',
  fontSize: '$1',
});

const CondTh = styled('th', {
  textAlign: 'left',
  padding: '$xs',
  fontSize: '$0',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$body',
  fontWeight: '$semibold',
  borderBottom: '1px solid $stroke',
});

const CondTd = styled('td', {
  padding: '$xs',
  color: '$light',
  borderBottom: '1px solid $stroke',
  verticalAlign: 'top',
});

const SkillPill = styled(Text, {
  display: 'inline-block',
  padding: '2px 8px',
  borderRadius: '$pill',
  background: '$bg03',
  border: '1px solid $stroke',
  color: '$light',
  fontSize: '$1',
});

const CodeRow = styled(Box, {
  position: 'relative',
  padding: '$sm',
  borderRadius: '$2',
  background: '$bg03',
  border: '1px solid $stroke',
});

const CodeText = styled(Text, {
  fontFamily: 'monospace',
  fontSize: '$1',
  color: '$light',
  wordBreak: 'break-all',
});

function gradientFor(seed: string): string {
  const h1 = Number.parseInt(seed.slice(0, 2), 16) * (360 / 256);
  const h2 = Number.parseInt(seed.slice(2, 4), 16) * (360 / 256);
  return `linear-gradient(135deg, hsl(${h1.toFixed(0)}, 60%, 24%), hsl(${h2.toFixed(0)}, 60%, 14%))`;
}

function statusLabel(status: string): string {
  switch (status) {
    case 'live':
      return '● live';
    case 'pending-registration':
      return '◐ pending registration';
    case 'degraded':
      return '◯ degraded';
    default:
      return status;
  }
}

export function MarketplaceDetail({ slug, onBack }: MarketplaceDetailProps) {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const { data, isLoading, isError, error, refetch } = useServiceDetail(slug);

  if (isLoading) {
    return (
      <Layout>
        <LoadingState message="Loading service…" />
      </Layout>
    );
  }
  if (isError && isStackUnreachable(error)) {
    return (
      <Layout>
        <StackUnreachableState
          detail={error.message}
          onRetry={() => refetch()}
        />
        {onBack ? (
          <GhostBtn type="button" onClick={onBack}>
            ← Back to marketplace
          </GhostBtn>
        ) : null}
      </Layout>
    );
  }
  if (isError || !data) {
    return (
      <Layout>
        <ErrorState
          message={error?.message ?? 'Failed to load service detail'}
        />
        {onBack ? (
          <GhostBtn type="button" onClick={onBack}>
            ← Back to marketplace
          </GhostBtn>
        ) : null}
      </Layout>
    );
  }

  return (
    <Layout>
      <Banner css={{ background: gradientFor(data.bannerSeed) }}>
        <BannerContent>
          <HeaderBlock>
            <Title>{data.name}</Title>
            <SubMono>{data.slug}</SubMono>
            <ChipRow css={{ marginTop: '$xs' }}>
              <Chip>{data.type}</Chip>
              <Chip>{statusLabel(data.status)}</Chip>
              {data.isDemo ? <Chip>demo</Chip> : null}
            </ChipRow>
          </HeaderBlock>
          <Flex css={{ gap: '$xs' }}>
            {onBack ? (
              <GhostBtn type="button" onClick={onBack}>
                ← Back
              </GhostBtn>
            ) : null}
            <PrimaryBtn type="button" onClick={() => setDrawerOpen(true)}>
              Buy access
            </PrimaryBtn>
          </Flex>
        </BannerContent>
      </Banner>

      <Grid>
        <Box css={{ display: 'flex', flexDirection: 'column', gap: '$md' }}>
          <Card>
            <CardTitle>Overview</CardTitle>
            <Description>{data.description || 'No description.'}</Description>
            <Box css={{ marginTop: '$xs' }}>
              <CardTitle css={{ marginBottom: '$xs' }}>Skills</CardTitle>
              {data.skills.length > 0 ? (
                <ChipRow>
                  {data.skills.map(skill => (
                    <SkillPill key={skill}>{skill}</SkillPill>
                  ))}
                </ChipRow>
              ) : (
                <KVLabel>None declared.</KVLabel>
              )}
            </Box>
          </Card>

          <Card>
            <CardTitle>Endpoint</CardTitle>
            <CodeRow>
              <Flex
                css={{
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  gap: '$xs',
                }}
              >
                <CodeText>{data.endpoint}</CodeText>
                <CopyButton text={data.endpoint} title="Copy endpoint" />
              </Flex>
            </CodeRow>
          </Card>

          <Card>
            <CardTitle>Status conditions</CardTitle>
            {data.conditions.length === 0 ? (
              <KVLabel>
                No conditions reported by the controller for this snapshot.
              </KVLabel>
            ) : (
              <ConditionTable>
                <thead>
                  <tr>
                    <CondTh>Type</CondTh>
                    <CondTh>Status</CondTh>
                    <CondTh>Message</CondTh>
                  </tr>
                </thead>
                <tbody>
                  {data.conditions.map(c => (
                    <tr key={c.type}>
                      <CondTd>{c.type}</CondTd>
                      <CondTd>{c.status}</CondTd>
                      <CondTd>{c.message || '—'}</CondTd>
                    </tr>
                  ))}
                </tbody>
              </ConditionTable>
            )}
          </Card>
        </Box>

        <Box css={{ display: 'flex', flexDirection: 'column', gap: '$md' }}>
          <Card>
            <CardTitle>Pricing</CardTitle>
            <KVRow>
              <KVLabel>Price</KVLabel>
              <KVValue>
                <ObolAmountSwitch
                  amount={data.price || '—'}
                  symbol={data.asset.symbol}
                  align="right"
                />
              </KVValue>
            </KVRow>
            <KVRow>
              <KVLabel>Unit</KVLabel>
              <KVValue>{data.priceUnit}</KVValue>
            </KVRow>
            <KVRow>
              <KVLabel>Atomic</KVLabel>
              <KVValue>{data.priceAtomicUnits || '—'}</KVValue>
            </KVRow>
          </Card>

          <Card>
            <CardTitle>Payment sample (402)</CardTitle>
            <KVRow>
              <KVLabel>Scheme</KVLabel>
              <KVValue>{data.paymentSample.scheme}</KVValue>
            </KVRow>
            <KVRow>
              <KVLabel>Network</KVLabel>
              <KVValue>{data.paymentSample.network}</KVValue>
            </KVRow>
            <KVRow>
              <KVLabel>Asset</KVLabel>
              <KVValue>{data.paymentSample.asset.symbol}</KVValue>
            </KVRow>
            <KVRow>
              <KVLabel>Decimals</KVLabel>
              <KVValue>{data.paymentSample.asset.decimals}</KVValue>
            </KVRow>
            <KVRow>
              <KVLabel>Asset address</KVLabel>
              <KVValue>{data.paymentSample.asset.address || '—'}</KVValue>
            </KVRow>
            <KVRow>
              <KVLabel>Max amount</KVLabel>
              <KVValue>{data.paymentSample.maxAmountRequired || '—'}</KVValue>
            </KVRow>
          </Card>

          <Card>
            <CardTitle>Discovery</CardTitle>
            <KVRow>
              <KVLabel>ERC-8004 well-known</KVLabel>
            </KVRow>
            <CodeRow>
              <Flex
                css={{
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  gap: '$xs',
                }}
              >
                <CodeText>{data.wellKnownUrl}</CodeText>
                <CopyButton
                  text={data.wellKnownUrl}
                  title="Copy well-known URL"
                />
              </Flex>
            </CodeRow>
          </Card>
        </Box>
      </Grid>

      <BuyDrawer
        item={data}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
      />
    </Layout>
  );
}
