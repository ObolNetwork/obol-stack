import React, { useState, useEffect, useRef } from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useDisconnect } from 'wagmi';
import { Box, Button, Flex, Text, styled, Image } from '@obolnetwork/obol-ui';
import { WalletModal } from '../WalletModal';

const ConnectedButton = styled('button', {
  display: 'flex',
  height: '40px',
  padding: '12px',
  justifyContent: 'center',
  alignItems: 'center',
  gap: '10px',
  borderRadius: '8px',
  border: '1px solid #2D4D53',
  opacity: 0.9,
  background: '#091011',
  backdropFilter: 'blur(2px)',
  color: '#E1E9EB',
  cursor: 'pointer',
  fontSize: '14px',
});

const AvatarContainer = styled('div', {
  width: '24px',
  height: '24px',
  borderRadius: '50%',
  backgroundImage: 'url(/avatar.png)',
  backgroundSize: 'cover',
  backgroundPosition: 'center',
  backgroundRepeat: 'no-repeat',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  fontSize: '10px',
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
                              borderRadius: '50%',
                            }}
                          />
                        )}
                      </AvatarContainer>
                      <Text
                        css={{
                          fontFamily: '$fonts$body',
                          fontSize: '16px',
                          fontWeight: '$semibold',
                          lineHeight: '24px',
                          color: '#E1E9EB',
                        }}
                      >
                        {account.displayName ||
                          `${account.address?.slice(0, 6)}...${account.address?.slice(-4)}`}
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
