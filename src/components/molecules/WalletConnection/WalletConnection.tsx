import { useState, useEffect, useRef } from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useDisconnect } from 'wagmi';
import { Box, Button, Flex, Text, styled, Image } from '@obolnetwork/obol-ui';
import { WalletModal } from '../WalletModal';
import { truncateAddress } from '../../../utils/address';
import { useWalletAssetBalances } from '../../../hooks/useWalletAssetBalances';

const ConnectedButton = styled('button', {
  display: 'flex',
  height: '$2xl',
  padding: '$xxxs $xs $xxxs $xxxs',
  justifyContent: 'center',
  alignItems: 'center',
  gap: '$xs',
  borderRadius: '$pill',
  border: '1px solid $bg05',
  opacity: 0.9,
  background: '$bg01',
  backdropFilter: 'blur(2px)',
  color: '$light',
  cursor: 'pointer',
  fontSize: '$3',
  transition: 'border-color 150ms ease, background 150ms ease',

  '&:hover': {
    borderColor: '$stroke',
    background: '$bg02',
  },
});

const ChainPill = styled('span', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxxs',
  padding: '$xxxs $xxs',
  borderRadius: '$pill',
  fontFamily: '$fonts$body',
  fontSize: '$1',
  fontWeight: '$medium',
  lineHeight: 1,
  whiteSpace: 'nowrap',

  variants: {
    tone: {
      green: {
        background: 'rgba(47, 228, 171, 0.15)',
        color: '$obolGreen',
      },
      amber: {
        background: 'rgba(250, 186, 90, 0.18)',
        color: '#FABA5A',
      },
      cyan: {
        background: 'rgba(60, 210, 221, 0.18)',
        color: '#3CD2DD',
      },
      neutral: {
        background: '$bg03',
        color: '$body',
      },
    },
  },
});

const ChainDot = styled('span', {
  width: 6,
  height: 6,
  borderRadius: '$round',
  background: 'currentColor',
  flexShrink: 0,
});

const BalanceChip = styled('span', {
  display: 'inline-flex',
  alignItems: 'baseline',
  gap: 4,
  padding: '$xxxs $xs',
  borderRadius: '$pill',
  background: '$bg03',
  fontFamily: '$fonts$mono',
  fontSize: '$2',
  fontWeight: '$medium',
  lineHeight: 1,
  color: '$light',
  whiteSpace: 'nowrap',

  variants: {
    muted: {
      true: { color: '$muted' },
    },
  },
});

const BalanceUnit = styled('span', {
  fontFamily: '$fonts$body',
  fontSize: '$1',
  color: '$body',
});

// Maps the connected EVM chain to a color tone for the wallet pill. Base
// Sepolia is intentionally amber so the testnet-vs-mainnet story reads at a
// glance.
function chainTone(chainId: number): 'green' | 'amber' | 'cyan' | 'neutral' {
  switch (chainId) {
    case 1: // Ethereum mainnet
      return 'green';
    case 8453: // Base
      return 'cyan';
    case 84532: // Base Sepolia
    case 11155111: // Ethereum Sepolia
      return 'amber';
    default:
      return 'neutral';
  }
}

function chainShortName(chain: { id: number; name?: string }): string {
  if (chain.name) return chain.name;
  return `chain ${chain.id}`;
}

const AvatarContainer = styled('div', {
  width: '$lg',
  height: '$lg',
  borderRadius: '$round',
  backgroundImage: 'url(/avatar.png)',
  backgroundSize: 'cover',
  backgroundPosition: 'center',
  backgroundRepeat: 'no-repeat',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  fontSize: '$1',
  fontWeight: 'bold',
});

function ConnectedWalletPill({
  account,
  chain,
  onClick,
}: {
  account: { address: string; displayName: string; ensAvatar?: string };
  chain: { id: number; name?: string; unsupported?: boolean };
  onClick: () => void;
}) {
  // Reuses the shared eRPC-backed balance hook: 60s polling, multicall3-batched,
  // routed through /rpc/<chain> (Traefik in prod, port-forward in dev). We only
  // surface the USDC entry for the *currently connected* chain in the pill.
  const { balances } = useWalletAssetBalances(account.address);
  const usdc = balances.find(
    b => b.chain.chainId === chain.id && b.token.symbol === 'USDC'
  );

  return (
    <ConnectedButton
      onClick={onClick}
      type="button"
      aria-label={`Wallet connected on ${chainShortName(chain)}`}
    >
      <ChainPill tone={chainTone(chain.id)}>
        <ChainDot />
        {chainShortName(chain)}
      </ChainPill>
      <AvatarContainer>
        {account.ensAvatar && (
          <Image
            src={account.ensAvatar}
            alt="ENS Avatar"
            width={24}
            height={24}
            style={{
              width: 24,
              height: 24,
              borderRadius: '$round',
            }}
          />
        )}
      </AvatarContainer>
      <Text
        css={{
          fontFamily: '$fonts$body',
          fontSize: '$4',
          fontWeight: '$semibold',
          lineHeight: '$base',
          color: '$light',
        }}
      >
        {account.displayName || truncateAddress(account.address ?? '')}
      </Text>
      <BalanceChip
        muted={!usdc}
        title={usdc ? undefined : 'USDC balance not loaded yet'}
      >
        {usdc ? usdc.formatted : '—'}
        <BalanceUnit>USDC</BalanceUnit>
      </BalanceChip>
    </ConnectedButton>
  );
}

export function WalletConnection() {
  const [isModalOpen, setIsModalOpen] = useState(false);
  const { disconnect } = useDisconnect();
  const modalRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        modalRef.current &&
        !modalRef.current.contains(event.target as Node)
      ) {
        setIsModalOpen(false);
      }
    };

    if (isModalOpen) {
      document.addEventListener('mousedown', handleClickOutside);
    }

    return () => {
      document.removeEventListener('mousedown', handleClickOutside);
    };
  }, [isModalOpen]);

  return (
    <ConnectButton.Custom>
      {({
        account,
        chain,
        openChainModal,
        openConnectModal,
        authenticationStatus,
        mounted,
      }: {
        account?: {
          address: string;
          displayName: string;
          ensAvatar?: string;
          ensName?: string;
        } | null;
        chain?: {
          id: number;
          name?: string;
          unsupported?: boolean;
        } | null;
        openChainModal: () => void;
        openConnectModal: () => void;
        authenticationStatus?: 'loading' | 'unauthenticated' | 'authenticated';
        mounted: boolean;
      }) => {
        const ready = mounted && authenticationStatus !== 'loading';
        const connected =
          ready &&
          account &&
          chain &&
          (!authenticationStatus || authenticationStatus === 'authenticated');

        return (
          <Box>
            {(() => {
              if (!connected) {
                return (
                  <Button onClick={openConnectModal} variant="primary">
                    Connect Wallet
                  </Button>
                );
              }

              if (chain.unsupported) {
                return (
                  <Button onClick={openChainModal} variant="primary" disabled>
                    Wrong network
                  </Button>
                );
              }

              return (
                <Box css={{ position: 'relative' }} ref={modalRef}>
                  <Flex css={{ alignItems: 'center' }}>
                    <ConnectedWalletPill
                      account={account}
                      chain={chain}
                      onClick={() => setIsModalOpen(true)}
                    />
                  </Flex>

                  <WalletModal
                    isOpen={isModalOpen}
                    onClose={() => setIsModalOpen(false)}
                    account={account}
                    onDisconnect={() => {
                      disconnect();
                      setIsModalOpen(false);
                    }}
                  />
                </Box>
              );
            })()}
          </Box>
        );
      }}
    </ConnectButton.Custom>
  );
}
