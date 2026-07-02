import React from 'react';
import { styled, theme, keyframes } from '@obolnetwork/obol-ui';
import { WalletClient } from 'viem';
import { addNetworkToWallet } from '@/lib/utils/wallet';
import { ChainName } from '@/types/dashboard';
import { AddCircle } from '@/icons';
import { CheckIcon } from '@radix-ui/react-icons';
import { toast } from 'react-toastify';
import { Tooltip } from '@/components/atoms/Tooltip';
import { Button } from '@/components/atoms/Button';
import { tones } from '@/lib/ui/tones';

export interface AddNetworkButtonProps {
  chainName: ChainName;
  walletClient?: WalletClient | null;
}

const spin = keyframes({
  from: { transform: 'rotate(0deg)' },
  to: { transform: 'rotate(360deg)' },
});

// Circular multi-state icon button — extends the canonical Button atom for
// focus-visible/transition consistency; circle shape and 32px hit target are
// deliberate (wallet-row affordance), state tints come from tones.*.
const StyledButton = styled(Button, {
  width: 32,
  height: 32,
  paddingLeft: 0,
  paddingRight: 0,
  borderWidth: '1.5px',
  borderRadius: '$round',
  transition:
    'border-color 150ms ease, background-color 150ms ease, transform 100ms ease',

  variants: {
    state: {
      idle: {
        borderColor: tones.success.border,
        color: tones.success.text,
        '&:hover:not(:disabled)': {
          borderColor: tones.success.text,
          background: tones.success.bg,
          transform: 'scale(1.08)',
        },
        '&:active': {
          transform: 'scale(0.96)',
        },
      },
      loading: {
        borderColor: tones.neutral.border,
        color: '$body',
        cursor: 'default',
      },
      success: {
        borderColor: tones.success.border,
        background: tones.success.bg,
        color: tones.success.text,
        cursor: 'default',
      },
      error: {
        borderColor: tones.danger.border,
        color: tones.danger.text,
        cursor: 'default',
      },
      disabled: {
        borderColor: tones.neutral.border,
        color: '$body',
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
        variant="bare"
        size="sm"
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
