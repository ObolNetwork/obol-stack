import React from 'react';
import { Image, Button, Flex, Text, styled, theme } from '@obolnetwork/obol-ui';
import { Close } from '@/icons';
import { useBalance, UseBalanceReturnType, useChainId } from 'wagmi';
import { formatUnits } from 'viem';
import { useTokenPrices } from '../../../hooks/useTokenPrices';
import { ChainIcon } from '../../atoms/ChainIcon';
import { getChainName } from '../../../types/dashboard';
import { TOKEN_ADDRESSES } from '../../../lib/constants/tokens';

interface WalletModalProps {
  isOpen: boolean;
  onClose: () => void;
  account: {
    address?: string;
    displayName?: string;
    ensAvatar?: string;
  };
  onDisconnect: () => void;
}

// Helper function to calculate and format USD balance
const calculateUsdBalance = (
  ethBalance: UseBalanceReturnType['data'],
  ethPrice: number
): string => {
  if (!ethBalance) return '0.000';

  const ethValue =
    parseFloat(formatUnits(ethBalance.value, ethBalance.decimals)) * ethPrice;
  return ethValue.toLocaleString('en-US', {
    minimumFractionDigits: 3,
    maximumFractionDigits: 3,
  });
};

// Helper functions for dynamic change display
const getChangeColor = (change: number): 'green' | 'orange' | 'body' => {
  if (change > 0) return 'green';
  if (change < 0) return 'orange';
  return 'body';
};

const getChangeArrow = (change: number): string => {
  if (change > 0) return '▲';
  if (change < 0) return '▼';
  return '';
};

const formatChange = (change: number): string => {
  const arrow = getChangeArrow(change);
  const percentage = Math.abs(change).toFixed(2);
  return `${arrow} ${percentage}%`;
};

const ModalContent = styled('div', {
  position: 'absolute',
  top: 'calc(100% + 16px)',
  right: 0,
  display: 'flex',
  width: '351px',
  padding: '$lg',
  flexDirection: 'column',
  alignItems: 'center',
  gap: '$sm',
  borderRadius: '8px',
  border: '1px solid $bg05',
  background: '$bg01',
  zIndex: 1000,
});

const CloseButton = styled('button', {
  position: 'absolute',
  top: '16px',
  right: '16px',
  background: 'transparent',
  border: 'none',
  cursor: 'pointer',
  width: '$sm',
  height: '$sm',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  padding: 0,

  '&:hover': {
    opacity: 0.7,
  },
});

const Avatar = styled('div', {
  width: '74px',
  height: '74px',
  borderRadius: '50%',
  backgroundImage: 'url(/avatar.png)',
  backgroundSize: 'cover',
  backgroundPosition: 'center',
  backgroundRepeat: 'no-repeat',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
});

const WalletName = styled(Text, {
  fontFamily: '$fonts$body',
  fontSize: '$4',
  fontWeight: '$semibold',
  lineHeight: '$base',
  color: '$white',
  textAlign: 'center',
});

const BalanceText = styled(Text, {
  fontFamily: '$fonts$body',
  fontSize: '$4',
  fontWeight: '$semibold',
  lineHeight: '$base',
  color: '$body',
  textAlign: 'center',
});

const TokenRow = styled('div', {
  display: 'flex',
  padding: '$xs',
  flexDirection: 'column',
  alignItems: 'flex-end',
  gap: '$xxs',
  alignSelf: 'stretch',
  borderRadius: '$3',
  background: '$bg02',
});

const TokenText = styled(Text, {
  fontFamily: '$fonts$body',
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$white',
  lineHeight: '$base',
});

const TokenInfo = styled(Flex, {
  alignItems: 'center',
  gap: '$xxs',
  flex: 0,
  fontFamily: '$fonts$body',
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$white',
  lineHeight: '$base',
});

const TokenRowFlex = styled(Flex, {
  width: '100%',
  alignItems: 'center',
  justifyContent: 'space-between',
  alignSelf: 'stretch',
});

const TokenChangeText = styled(Text, {
  textAlign: 'center',
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontWeight: '$medium',
  lineHeight: '$shorter',

  variants: {
    color: {
      green: {
        color: '$obolGreen',
      },
      orange: {
        color: '$orange',
      },
      body: {
        color: '$body',
      },
    },
  },

  defaultVariants: {
    color: 'body',
  },
});

export function WalletModal({
  isOpen,
  onClose,
  account,
  onDisconnect,
}: WalletModalProps) {
  // Get ETH balance
  const { data: ethBalance } = useBalance({
    address: account.address as `0x${string}`,
  });

  // Get Obol token balance
  const { data: obolBalance } = useBalance({
    address: account.address as `0x${string}`,
    token: TOKEN_ADDRESSES.OBOL,
  });

  const chainId = useChainId();

  // Get token prices using combined hook
  const { ethPrice, ethChange24h, obolPrice, obolChange24h } = useTokenPrices();

  // Calculate balances in USD
  const ethBalanceUsd = calculateUsdBalance(ethBalance, ethPrice);
  const obolBalanceUsd = calculateUsdBalance(obolBalance, obolPrice);

  // Calculate total balance in USD
  const totalBalanceUsd = (
    parseFloat(ethBalanceUsd.replace(/,/g, '')) +
    parseFloat(obolBalanceUsd.replace(/,/g, ''))
  ).toLocaleString('en-US', {
    minimumFractionDigits: 3,
    maximumFractionDigits: 3,
  });

  if (!isOpen) return null;

  const handleDisconnect = () => {
    onDisconnect();
    onClose();
  };
  return (
    <ModalContent>
      <CloseButton onClick={onClose}>
        <Close size="xs" color={theme.colors.body.value} />
      </CloseButton>

      <Avatar>
        {account.ensAvatar && (
          <Image
            src={account.ensAvatar}
            alt="ENS Avatar"
            width={74}
            height={74}
            style={{
              width: 74,
              height: 74,
              borderRadius: '50%',
            }}
          />
        )}
      </Avatar>

      <WalletName>
        {account.displayName ||
          (account.address
            ? `${account.address.slice(0, 6)}...${account.address.slice(-4)}`
            : 'Unknown')}
      </WalletName>

      <BalanceText>$ {totalBalanceUsd}</BalanceText>

      <Flex css={{ width: '100%', flexDirection: 'column', gap: '8px' }}>
        <TokenRow>
          <TokenRowFlex>
            <TokenInfo>
              <ChainIcon chain={getChainName(chainId)} size={32} />
              <TokenText>Ethereum</TokenText>
            </TokenInfo>
            <TokenText>$ {ethBalanceUsd}</TokenText>
          </TokenRowFlex>

          <TokenRowFlex>
            <TokenChangeText color={getChangeColor(ethChange24h)}>
              {formatChange(ethChange24h)}
            </TokenChangeText>
            <TokenChangeText color="body">
              {ethBalance
                ? parseFloat(
                    formatUnits(ethBalance.value, ethBalance.decimals)
                  ).toFixed(4)
                : '0.0000'}{' '}
              ETH
            </TokenChangeText>
          </TokenRowFlex>
        </TokenRow>

        <TokenRow>
          <TokenRowFlex>
            <TokenInfo>
              <Image
                src="/obol-logo.png"
                alt="Obol"
                width={32}
                height={32}
                style={{
                  width: 32,
                  height: 32,
                  borderRadius: '50%',
                }}
              />
              <TokenText>Obol</TokenText>
            </TokenInfo>
            <TokenText>$ {obolBalanceUsd}</TokenText>
          </TokenRowFlex>

          <TokenRowFlex>
            <TokenChangeText color={getChangeColor(obolChange24h)}>
              {formatChange(obolChange24h)}
            </TokenChangeText>
            <TokenChangeText color="body">
              {obolBalance
                ? parseFloat(
                    formatUnits(obolBalance.value, obolBalance.decimals)
                  ).toFixed(4)
                : '0.0000'}{' '}
              OBOL
            </TokenChangeText>
          </TokenRowFlex>
        </TokenRow>
      </Flex>

      <Button fullWidth color="secondary" onClick={handleDisconnect}>
        Disconnect
      </Button>
    </ModalContent>
  );
}
