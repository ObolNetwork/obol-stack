'use client';

import { useEffect, useId, useMemo, useState, type ChangeEvent } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Cross2Icon } from '@radix-ui/react-icons';

import { Button } from '@/components/atoms/Button';
import { CopyButton } from '@/components/atoms/CopyButton';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import {
  buyPyCommand,
  computePreflight,
  kubectlBuyCommand,
  litellmCurlExample,
  obolBuyInferenceCommand,
} from '@/lib/marketplace/buy-command';
import type { ServiceListItem } from '@/lib/marketplace/types';

export interface BuyDrawerProps {
  item: ServiceListItem | null;
  open: boolean;
  onClose: () => void;
}

const Backdrop = styled(Box, {
  position: 'fixed',
  inset: 0,
  zIndex: 40,
  background: 'rgba(9, 16, 17, 0.6)',
  backdropFilter: 'blur(2px)',
});

const Panel = styled(Box, {
  position: 'fixed',
  top: 0,
  right: 0,
  bottom: 0,
  zIndex: 41,
  width: 520,
  maxWidth: '100vw',
  display: 'flex',
  flexDirection: 'column',
  background: '#091011',
  borderLeft: '1px solid $stroke',
  boxShadow: '-8px 0 24px rgba(0, 0, 0, 0.4)',
  overflow: 'hidden',
});

const Header = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$xs',
  padding: '$md',
  borderBottom: '1px solid $stroke',
});

const TitleBlock = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: 2,
});

const Title = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
});

const Subtitle = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
});

const Body = styled(Box, {
  flex: 1,
  overflow: 'auto',
  padding: '$md',
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

const Section = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const SectionTitle = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$body',
});

const PreflightGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr 1fr',
  gap: '$xs',
  padding: '$sm',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
});

const Row = styled(Flex, {
  flexDirection: 'column',
  gap: 2,
});

const RowLabel = styled(Text, {
  fontSize: '$0',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
});

const RowValue = styled(Text, {
  fontSize: '$2',
  color: '$light',
  fontFamily: 'monospace',
});

const CountRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
});

// Count preset chips — Button atom (secondary/sm/pill) with a selected
// state; flex:1 so the presets share the row with the free-form input.
const CountBtn = styled(Button, {
  flex: 1,

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        borderColor: '$obolGreen',
        color: '$obolGreen',
        '&:hover:not(:disabled)': { background: '$obolMidGreen' },
      },
    },
  },

  defaultVariants: {
    variant: 'secondary',
    size: 'sm',
    pill: true,
  },
});

const CountInput = styled('input', {
  flex: 1,
  height: 28,
  padding: '0 $sm',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  color: '$light',
  fontSize: '$1',
  fontFamily: 'monospace',
  '&:focus': { outline: 'none', borderColor: '$obolGreen' },
});

const CodeBlock = styled('pre', {
  position: 'relative',
  margin: 0,
  padding: '$sm',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
  color: '$light',
  fontFamily: 'monospace',
  fontSize: '$1',
  lineHeight: 1.5,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
});

const CopyAffordance = styled(Box, {
  position: 'absolute',
  top: '$xs',
  right: '$xs',
});

const PRESET_COUNTS = [1, 10, 100] as const;

export function BuyDrawer({ item, open, onClose }: BuyDrawerProps) {
  const [count, setCount] = useState<number>(10);
  const titleId = useId();

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    document.body.style.overflow = 'hidden';
    return () => {
      window.removeEventListener('keydown', onKey);
      document.body.style.overflow = '';
    };
  }, [open, onClose]);

  const preflight = useMemo(
    () =>
      item
        ? computePreflight(item, count)
        : { count, totalHuman: '0', totalAtomic: '0' },
    [item, count]
  );

  const obolCmd = useMemo(
    () => (item ? obolBuyInferenceCommand(item, count, { yes: true }) : ''),
    [item, count]
  );
  const buyCmd = useMemo(
    () => (item ? buyPyCommand(item, count) : ''),
    [item, count]
  );
  const execCmd = useMemo(
    () => (item ? kubectlBuyCommand(item, count) : ''),
    [item, count]
  );
  const curlCmd = useMemo(() => (item ? litellmCurlExample(item) : ''), [item]);

  if (!open || !item) return null;

  return (
    <>
      <Backdrop onClick={onClose} />
      <Panel role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <Header>
          <TitleBlock>
            <Title id={titleId}>Buy access to {item.name}</Title>
            <Subtitle>{item.slug}</Subtitle>
          </TitleBlock>
          <Button
            type="button"
            variant="ghost"
            iconOnly
            aria-label="Close"
            title="Close (Esc)"
            onClick={onClose}
          >
            <Cross2Icon width={16} height={16} />
          </Button>
        </Header>
        <Body>
          <Section>
            <SectionTitle>Preflight</SectionTitle>
            <PreflightGrid>
              <Row>
                <RowLabel>Endpoint</RowLabel>
                <RowValue>{item.endpoint}</RowValue>
              </Row>
              <Row>
                <RowLabel>Model</RowLabel>
                <RowValue>{item.modelOrRuntime || '—'}</RowValue>
              </Row>
              <Row>
                <RowLabel>Price / {item.priceUnit}</RowLabel>
                <RowValue>
                  <ObolAmountSwitch
                    amount={item.price || '—'}
                    symbol={item.asset.symbol}
                  />
                </RowValue>
              </Row>
              <Row>
                <RowLabel>Network</RowLabel>
                <RowValue>{item.network.alias || item.network.caip2}</RowValue>
              </Row>
            </PreflightGrid>
          </Section>

          <Section>
            <SectionTitle>Count</SectionTitle>
            <CountRow>
              {PRESET_COUNTS.map(preset => (
                <CountBtn
                  key={preset}
                  type="button"
                  active={count === preset}
                  onClick={() => setCount(preset)}
                >
                  {preset}
                </CountBtn>
              ))}
              <CountInput
                type="number"
                min={1}
                step={1}
                value={count}
                onChange={(e: ChangeEvent<HTMLInputElement>) => {
                  const n = Number.parseInt(e.target.value, 10);
                  setCount(Number.isFinite(n) && n > 0 ? n : 1);
                }}
              />
            </CountRow>
            <Flex
              css={{
                justifyContent: 'space-between',
                marginTop: '$xs',
                gap: '$xs',
              }}
            >
              <RowLabel>Total</RowLabel>
              <RowValue>
                <ObolAmountSwitch
                  amount={preflight.totalHuman}
                  symbol={item.asset.symbol}
                />
              </RowValue>
            </Flex>
            <Flex
              css={{
                justifyContent: 'space-between',
                gap: '$xs',
              }}
            >
              <RowLabel>Atomic units</RowLabel>
              <RowValue>{preflight.totalAtomic}</RowValue>
            </Flex>
          </Section>

          <Section>
            <SectionTitle>Recommended — run on your Mac</SectionTitle>
            <Text css={{ fontSize: '$1', color: '$body', marginBottom: '$xs' }}>
              Pre-signs x402 payment auths via your agent wallet and wires
              LiteLLM <code>paid/&lt;model&gt;</code>. Matches{' '}
              <code>obol buy inference</code> in the stack CLI.
            </Text>
            <Box css={{ position: 'relative' }}>
              <CodeBlock>{obolCmd}</CodeBlock>
              <CopyAffordance>
                <CopyButton text={obolCmd} title="Copy obol buy inference" />
              </CopyAffordance>
            </Box>
          </Section>

          <Section>
            <SectionTitle>Advanced — kubectl exec</SectionTitle>
            <Box css={{ position: 'relative' }}>
              <CodeBlock>{execCmd}</CodeBlock>
              <CopyAffordance>
                <CopyButton text={execCmd} title="Copy kubectl command" />
              </CopyAffordance>
            </Box>
          </Section>

          <Section>
            <SectionTitle>Advanced — inside the agent pod</SectionTitle>
            <Box css={{ position: 'relative' }}>
              <CodeBlock>{buyCmd}</CodeBlock>
              <CopyAffordance>
                <CopyButton text={buyCmd} title="Copy buy.py command" />
              </CopyAffordance>
            </Box>
          </Section>

          <Section>
            <SectionTitle>Validate via LiteLLM (after buy)</SectionTitle>
            <Box css={{ position: 'relative' }}>
              <CodeBlock>{curlCmd}</CodeBlock>
              <CopyAffordance>
                <CopyButton text={curlCmd} title="Copy curl example" />
              </CopyAffordance>
            </Box>
          </Section>
        </Body>
      </Panel>
    </>
  );
}
