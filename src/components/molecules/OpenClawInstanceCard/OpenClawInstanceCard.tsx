'use client';

import {
  Box,
  Flex,
  Text,
  Button,
  Link,
  styled,
  OpenInNew,
} from '@obolnetwork/obol-ui';
import { StatusPill } from '../../atoms/StatusIndicator';
import { CopyButton } from '../../atoms/CopyButton/CopyButton';
import { SellInferenceModal } from '../SellInferenceModal';
import {
  useOpenClawStatus,
  useWalletMetadata,
  useTunnelURL,
} from '../../../hooks/useOpenClaw';
import { useAddressBalance } from '../../../hooks/useAddressBalance';
import type { OpenClawInstance } from '@/types/openclaw';

/** Strip the LiteLLM routing prefix that leaks from the backend overlay. */
function displayModelName(raw: string): string {
  return raw.replace(/^openai\//, '');
}

/** Infer the real provider from the model name (not the LiteLLM routing prefix). */
function inferProvider(modelName: string): string {
  const name = displayModelName(modelName);
  if (name.includes('claude')) return 'Anthropic';
  if (/^(gpt|o[1-4])/.test(name)) return 'OpenAI';
  return 'Ollama';
}

interface OpenClawInstanceCardProps {
  instance: OpenClawInstance;
  onSelect?: (instance: OpenClawInstance) => void;
}

const CardContainer = styled(Box, {
  padding: '$sm $md',
  borderRadius: '$2',
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  transition: 'border-color 0.2s ease',

  '&:hover': {
    borderColor: '$body',
  },
});

const TopRow = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  marginBottom: '$lg',
});

const InstanceName = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
});

const MetaItem = styled(Flex, {
  alignItems: 'center',
  gap: '$xxxs',
  marginBottom: '$xxs',
  marginTop: '$xs',
});

const MetaLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
});

const MetaValue = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
});

const ActionsRow = styled(Flex, {
  paddingTop: '$lg',
  alignItems: 'center',
  gap: '$xs',
});

const DashboardLinkWrapper = styled(Flex, {
  position: 'relative',
  alignItems: 'center',
  '&:hover .dashboard-tooltip': {
    opacity: 1,
    visibility: 'visible',
  },
});

const BalanceText = styled(Text, {
  fontSize: '$1',
  color: '$body',
  whiteSpace: 'nowrap',
});

const KeysBadge = styled(Text, {
  fontSize: '10px',
  fontWeight: '$semibold',
  color: '$obolGreen',
  backgroundColor: 'rgba(47, 228, 171, 0.08)',
  border: '1px solid rgba(47, 228, 171, 0.25)',
  borderRadius: '4px',
  padding: '1px 5px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
});

function truncateAddress(address: string): string {
  if (address.length <= 10) return address;
  return `${address.slice(0, 6)}...${address.slice(-4)}`;
}

const SellDivider = styled(Box, {
  height: '1px',
  backgroundColor: '$stroke',
  marginTop: '$md',
});

const SellCtaRow = styled(Flex, {
  paddingTop: '$md',
  alignItems: 'center',
  justifyContent: 'space-between',
});

const SellLabel = styled(Text, {
  fontSize: '$2',
  color: '$body',
  lineHeight: 1.4,
});

const SellButton = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '6px',
  padding: '$xxxs $sm',
  borderRadius: '$pill',
  border: '1px solid rgba(47, 228, 171, 0.3)',
  background: 'rgba(47, 228, 171, 0.08)',
  color: '#2FE4AB',
  fontSize: '$2',
  fontWeight: '$semibold',
  cursor: 'pointer',
  transition: 'all 0.15s ease',
  whiteSpace: 'nowrap',

  '&:hover': {
    background: 'rgba(47, 228, 171, 0.15)',
    borderColor: 'rgba(47, 228, 171, 0.5)',
  },
});

export function OpenClawInstanceCard({
  instance,
  onSelect,
}: OpenClawInstanceCardProps) {
  const { status, loading } = useOpenClawStatus(instance.id);

  const displayStatus = loading
    ? 'pending'
    : status?.podStatus?.toLowerCase() === 'running'
      ? 'running'
      : status?.podStatus?.toLowerCase() === 'pending'
        ? 'pending'
        : status
          ? 'error'
          : 'unknown';

  const { metadata } = useWalletMetadata(instance.id);

  const primaryAddress = metadata?.addresses?.[0]?.address ?? null;
  const { balance: balanceInfo } = useAddressBalance(primaryAddress);
  const { tunnelURL } = useTunnelURL();

  const dashboardUrl = tunnelURL
    ? tunnelURL
    : `http://${instance.namespace}.obol.stack`;

  return (
    <CardContainer>
      <TopRow>
        <Flex css={{ alignItems: 'center', gap: '$xs' }}>
          <InstanceName>
            {instance.id === 'default' ? 'OpenClaw' : instance.id}
          </InstanceName>
        </Flex>
        <StatusPill
          status={
            displayStatus === 'running'
              ? 'synced'
              : displayStatus === 'pending'
                ? 'syncing'
                : displayStatus === 'error'
                  ? 'error'
                  : 'unsynced'
          }
          label={loading ? 'Loading...' : status?.podStatus || 'Unknown'}
        />
      </TopRow>

      <MetaItem>
        <MetaLabel>Host:</MetaLabel>
        <MetaValue>{instance.hostname}</MetaValue>
      </MetaItem>
      {!loading && status?.model && (
        <MetaItem>
          <MetaLabel>Model:</MetaLabel>
          <MetaValue>{displayModelName(status.model)}</MetaValue>
        </MetaItem>
      )}
      {!loading && status && (
        <MetaItem>
          <MetaLabel>LLM:</MetaLabel>
          <MetaValue>
            {status.llmConnected === undefined
              ? 'Checking...'
              : status.llmConnected
                ? `Connected${status.model ? ` (${inferProvider(status.model)})` : ''}`
                : `Error${status.llmError ? ` — ${status.llmError}` : ''}`}
          </MetaValue>
        </MetaItem>
      )}
      {metadata && metadata.addresses.length > 0 && (
        <MetaItem>
          <MetaLabel>Wallet:</MetaLabel>
          <MetaValue>
            {truncateAddress(metadata.addresses[0].address)}
          </MetaValue>
          <CopyButton
            text={metadata.addresses[0].address}
            title="Copy address"
          />
          {metadata.count > 1 && (
            <KeysBadge>+{metadata.count - 1} keys</KeysBadge>
          )}
          {balanceInfo && (
            <BalanceText>
              {balanceInfo.balance} ETH ({balanceInfo.network})
            </BalanceText>
          )}
        </MetaItem>
      )}

      <ActionsRow>
        <Button
          disabled={displayStatus !== 'running'}
          color="primary"
          onClick={() => onSelect?.(instance)}
          css={{ fontSize: '$3', height: '$sm' }}
        >
          Chat
        </Button>
        <DashboardLinkWrapper>
          <Link
            href={dashboardUrl}
            target="_blank"
            rel="noopener noreferrer"
            css={{
              fontSize: '13px',
            }}
          >
            Dashboard
            <OpenInNew size="sm" />
          </Link>
          <Text
            className="dashboard-tooltip"
            css={{
              position: 'absolute',
              left: 'calc(100% + 8px)',
              top: '50%',
              transform: 'translateY(-50%)',
              background: '$bg02',
              border: '1px solid $bg04',
              borderRadius: '$1',
              padding: '$xxs $xs',
              fontSize: '11px',
              color: '$textLight',
              lineHeight: 1.3,
              width: 'max-content',
              maxWidth: '220px',
              opacity: 0,
              visibility: 'hidden',
              transition: 'opacity 0.2s ease',
              pointerEvents: 'none',
              zIndex: 10,
            }}
          >
            Connection issues? Try: obol openclaw dashboard
          </Text>
        </DashboardLinkWrapper>
      </ActionsRow>

      {instance.id === 'default' && displayStatus === 'running' && (
        <>
          <SellDivider />
          <SellCtaRow>
            <SellLabel>Sell access to your models</SellLabel>
            <SellInferenceModal
              defaultWallet={primaryAddress ?? undefined}
              trigger={
                <SellButton>
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <circle cx="12" cy="12" r="10" />
                    <line x1="12" y1="8" x2="12" y2="16" />
                    <line x1="8" y1="12" x2="16" y2="12" />
                  </svg>
                  Sell
                </SellButton>
              }
            />
          </SellCtaRow>
        </>
      )}
    </CardContainer>
  );
}
