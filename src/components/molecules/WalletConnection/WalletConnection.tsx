import { useState, useEffect, useRef } from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useDisconnect } from 'wagmi';
import { Box, Button, Flex, Text, styled, Image } from '@obolnetwork/obol-ui';
import { WalletModal } from '../WalletModal';
import { truncateAddress } from '../../../utils/address';

const ConnectedButton = styled('button', {
  display: 'flex',
  height: '$2xl',
  padding: '$xs',
  justifyContent: 'center',
  alignItems: 'center',
  gap: '$xs',
  borderRadius: '$3',
  border: '1px solid $bg05',
  opacity: 0.9,
  background: '$bg01',
  backdropFilter: 'blur(2px)',
  color: '$light',
  cursor: 'pointer',
  fontSize: '$3',
});

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
                    <ConnectedButton
                      onClick={() => setIsModalOpen(true)}
                      type="button"
                    >
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
                        {account.displayName ||
                          truncateAddress(account.address ?? '')}
                      </Text>
                    </ConnectedButton>
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
