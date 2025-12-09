import React from 'react';
import { styled, theme } from '@obolnetwork/obol-ui';
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

const StyledButton = styled('div', {
  display: 'flex',
  width: 'max-content',
  alignItems: 'center',
  justifyContent: 'center',
  cursor: 'pointer',
  color: '$light',
});

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
      console.warn('Wallet not connected');
      toast.error('Wallet not connected');
      return;
    }
    if (isLoading || isSuccess || isError) {
      return;
    }
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
  const color = !walletClient
    ? theme.colors.body.value
    : isError
      ? theme.colors.test.value
      : isSuccess
        ? theme.colors.obolGreen.value
        : isLoading
          ? theme.colors.body.value
          : theme.colors.obolGreen.value;

  const label = !walletClient
    ? 'Wallet not connected'
    : isError
      ? 'Error adding network'
      : isSuccess
        ? 'Network added to wallet'
        : isLoading
          ? 'Adding network...'
          : 'Add network to wallet';

  const isDisabled = isLoading || isSuccess || isError || !walletClient;

  return (
    <Tooltip content={label} side="top">
      <StyledButton
        onClick={handleClick}
        css={{ cursor: isDisabled ? 'default' : 'pointer' }}
      >
        {isSuccess ? (
          <CheckIcon
            width={24}
            height={24}
            color={theme.colors.obolGreen.value}
          />
        ) : (
          <AddCircle size="s" color={color} />
        )}
      </StyledButton>
    </Tooltip>
  );
}
