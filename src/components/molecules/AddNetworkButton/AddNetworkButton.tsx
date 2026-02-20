import React from 'react';
import { styled, theme, keyframes } from '@obolnetwork/obol-ui';
import { WalletClient } from 'viem';
import { addNetworkToWallet } from '@/lib/utils/wallet';
import { ChainName } from '@/types/dashboard';
import { AddCircle } from '@/icons';
import { CheckIcon } from '@radix-ui/react-icons';
import { toast } from 'react-toastify';
import { Tooltip } from '@/components/atoms/Tooltip';

export interface AddNetworkButtonProps {
  chainName: ChainName;
  walletClient?: WalletClient | null;
}

const spin = keyframes({
  from: { transform: 'rotate(0deg)' },
  to: { transform: 'rotate(360deg)' },
});

const StyledButton = styled('button', {
  display: 'flex',
  width: 32,
  height: 32,
  alignItems: 'center',
  justifyContent: 'center',
  border: '1.5px solid transparent',
  borderRadius: '$round',
  background: 'transparent',
  cursor: 'pointer',
  padding: 0,
  transition:
    'border-color 150ms ease, background 150ms ease, transform 100ms ease',
  outline: 'none',

  '&:focus-visible': {
    boxShadow: `0 0 0 2px ${theme.colors.obolGreen.value}44`,
  },

  variants: {
    state: {
      idle: {
        borderColor: `${theme.colors.obolGreen.value}55`,
        color: theme.colors.obolGreen.value,
        '&:hover': {
          borderColor: theme.colors.obolGreen.value,
          background: `${theme.colors.obolGreen.value}18`,
          transform: 'scale(1.08)',
        },
        '&:active': {
          transform: 'scale(0.96)',
        },
      },
      loading: {
        borderColor: `${theme.colors.body.value}55`,
        color: theme.colors.body.value,
        cursor: 'default',
      },
      success: {
        borderColor: `${theme.colors.obolGreen.value}88`,
        background: `${theme.colors.obolGreen.value}18`,
        color: theme.colors.obolGreen.value,
        cursor: 'default',
      },
      error: {
        borderColor: `${theme.colors.test.value}55`,
        color: theme.colors.test.value,
        cursor: 'default',
      },
      disabled: {
        borderColor: `${theme.colors.body.value}33`,
        color: theme.colors.body.value,
        cursor: 'not-allowed',
        opacity: 0.5,
      },
    },
  },
  defaultVariants: { state: 'idle' },
});

const SpinnerRing = styled('svg', {
  animation: `${spin} 800ms linear infinite`,
});

function LoadingSpinner({ color }: { color: string }) {
  return (
    <SpinnerRing
      width={18}
      height={18}
      viewBox="0 0 18 18"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <circle
        cx="9"
        cy="9"
        r="7"
        stroke={color}
        strokeWidth="2"
        strokeOpacity="0.25"
      />
      <path
        d="M16 9A7 7 0 0 0 9 2"
        stroke={color}
        strokeWidth="2"
        strokeLinecap="round"
      />
    </SpinnerRing>
  );
}

export function AddNetworkButton({
  chainName,
  walletClient,
}: AddNetworkButtonProps) {
  const [isLoading, setIsLoading] = React.useState<boolean>(false);
  const [isError, setIsError] = React.useState<boolean>(false);
  const [isSuccess, setIsSuccess] = React.useState<boolean>(false);

  const handleClick = async (event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();

    if (!walletClient) {
      toast.error('Wallet not connected');
      return;
    }
    if (isLoading || isSuccess || isError) return;

    setIsLoading(true);
    setIsError(false);
    setIsSuccess(false);

    try {
      await addNetworkToWallet(walletClient, chainName);
      setIsSuccess(true);
    } catch (error) {
      console.error('Error adding network:', error);
      toast.error('Error adding network: ' + error);
      setIsError(true);
    } finally {
      setIsLoading(false);
    }
  };

  const buttonState = !walletClient
    ? 'disabled'
    : isError
      ? 'error'
      : isSuccess
        ? 'success'
        : isLoading
          ? 'loading'
          : 'idle';

  const label = !walletClient
    ? 'Wallet not connected'
    : isError
      ? 'Error adding network'
      : isSuccess
        ? 'Network added to wallet'
        : isLoading
          ? 'Adding network...'
          : 'Add network to wallet';

  const iconColor = {
    idle: theme.colors.obolGreen.value,
    loading: theme.colors.body.value,
    success: theme.colors.obolGreen.value,
    error: theme.colors.test.value,
    disabled: theme.colors.body.value,
  }[buttonState];

  return (
    <Tooltip content={label} side="top">
      <StyledButton
        type="button"
        state={buttonState}
        onClick={handleClick}
        aria-label={label}
        aria-disabled={buttonState !== 'idle'}
      >
        {isSuccess ? (
          <CheckIcon width={18} height={18} color={iconColor} />
        ) : isLoading ? (
          <LoadingSpinner color={iconColor} />
        ) : (
          <AddCircle size="s" color={iconColor} />
        )}
      </StyledButton>
    </Tooltip>
  );
}
