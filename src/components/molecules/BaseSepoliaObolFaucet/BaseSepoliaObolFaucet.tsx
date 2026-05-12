'use client';

import { useEffect, useState } from 'react';
import { Box, Button, Flex, Text, styled } from '@obolnetwork/obol-ui';
import {
  useAccount,
  useChainId,
  useSwitchChain,
  useWaitForTransactionReceipt,
  useWriteContract,
} from 'wagmi';
import { ChainIcon } from '../../atoms/ChainIcon';
import { CopyButton } from '../../atoms/CopyButton';
import {
  BASE_SEPOLIA_OBOL_DEFAULT,
  BASE_SEPOLIA_OBOL_FAUCET_ABI,
  type BaseSepoliaObolConfig,
} from '../../../lib/constants/base-sepolia-obol';
import { truncateAddress } from '../../../utils/address';

const FaucetCard = styled(Box, {
  margin: '$xs $sm $sm',
  padding: '$sm',
  borderRadius: '$3',
  border: '1px solid $stroke',
  background:
    'linear-gradient(135deg, rgba(47, 228, 171, 0.10), rgba(145, 103, 228, 0.08))',
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
});

const Label = styled(Text, {
  color: '$body',
  fontSize: '$1',
  fontWeight: '$semibold',
  letterSpacing: '$wider',
  textTransform: 'uppercase',
});

const Value = styled(Text, {
  color: '$light',
  fontFamily: 'monospace',
  fontSize: '$2',
  wordBreak: 'break-all',
});

const Description = styled(Text, {
  color: '$body',
  fontSize: '$2',
  lineHeight: '$base',
});

const StatusText = styled(Text, {
  color: '$body',
  fontSize: '$2',
  lineHeight: '$base',

  variants: {
    tone: {
      success: { color: '$obolGreen' },
      warning: { color: '$orange' },
      muted: { color: '$body' },
    },
  },

  defaultVariants: {
    tone: 'muted',
  },
});

const TxLink = styled('a', {
  color: '$obolGreen',
  fontSize: '$2',
  textDecoration: 'none',

  '&:hover': {
    textDecoration: 'underline',
  },
});

const DetailGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '88px 1fr',
  columnGap: '$sm',
  rowGap: '$xxs',
  alignItems: 'center',
});

export function BaseSepoliaObolFaucet() {
  const { address, isConnected } = useAccount();
  const chainId = useChainId();
  const { switchChain, isPending: isSwitchingChain } = useSwitchChain();
  const {
    writeContract,
    data: hash,
    error: writeError,
    isPending: isWritePending,
  } = useWriteContract();
  const { isLoading: isConfirming, isSuccess: isConfirmed } =
    useWaitForTransactionReceipt({ hash });
  const [localError, setLocalError] = useState<string | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);
  const [config, setConfig] = useState<BaseSepoliaObolConfig>(
    BASE_SEPOLIA_OBOL_DEFAULT
  );

  useEffect(() => {
    let cancelled = false;

    async function loadConfig() {
      try {
        const response = await fetch('/api/config/base-sepolia-obol', {
          cache: 'no-store',
        });

        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`);
        }

        const nextConfig = (await response.json()) as BaseSepoliaObolConfig;
        if (!cancelled) {
          setConfig({ ...BASE_SEPOLIA_OBOL_DEFAULT, ...nextConfig });
          setConfigError(null);
        }
      } catch {
        if (!cancelled) {
          setConfigError('Unable to load Base Sepolia OBOL faucet config.');
        }
      }
    }

    loadConfig();

    return () => {
      cancelled = true;
    };
  }, []);

  const faucetAddress = config.faucetAddress;
  const isBaseSepolia = chainId === config.chainId;
  const isBusy = isSwitchingChain || isWritePending || isConfirming;

  const buttonLabel = (() => {
    if (!faucetAddress) return 'Faucet not configured';
    if (!isConnected) return 'Connect wallet to claim';
    if (!isBaseSepolia) return 'Switch to Base Sepolia';
    if (isSwitchingChain) return 'Switching network...';
    if (isWritePending) return 'Open wallet to confirm';
    if (isConfirming) return 'Claiming OBOL...';
    if (isConfirmed) return 'Claim again';
    return `Claim ${config.claimAmountLabel}`;
  })();

  const handleClaim = () => {
    setLocalError(null);

    if (!faucetAddress) {
      setLocalError(
        'Configure BASE_SEPOLIA_OBOL_FAUCET_ADDRESS to enable claims.'
      );
      return;
    }

    if (!address) {
      setLocalError('Connect a wallet before claiming Base Sepolia OBOL.');
      return;
    }

    if (!isBaseSepolia) {
      switchChain({ chainId: config.chainId });
      return;
    }

    writeContract({
      address: faucetAddress,
      abi: BASE_SEPOLIA_OBOL_FAUCET_ABI,
      functionName: 'claim',
      args: [address],
    });
  };

  const shortFaucetAddress = faucetAddress
    ? truncateAddress(faucetAddress)
    : 'Not configured';
  const shortTokenAddress = config.tokenAddress
    ? truncateAddress(config.tokenAddress)
    : 'Pending deployment';
  const txUrl = hash ? `${config.explorerUrl}/tx/${hash}` : null;
  const errorMessage = localError ?? writeError?.message ?? configError ?? null;

  return (
    <FaucetCard data-testid="base-sepolia-obol-faucet">
      <Flex
        css={{
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '$sm',
        }}
      >
        <Flex css={{ alignItems: 'center', gap: '$xs', width: 'auto' }}>
          <ChainIcon chain="base-sepolia" size={24} />
          <Box>
            <Text
              css={{
                color: '$light',
                fontWeight: '$semibold',
                lineHeight: '$base',
              }}
            >
              Base Sepolia OBOL Faucet
            </Text>
            <Description>Claim test OBOL for x402 smoke tests.</Description>
          </Box>
        </Flex>
      </Flex>

      <DetailGrid>
        <Label>Network</Label>
        <Value>{config.chainName}</Value>

        <Label>Token</Label>
        <Flex css={{ alignItems: 'center', gap: '$xxs', minWidth: 0 }}>
          <Value>{shortTokenAddress}</Value>
          {config.tokenAddress && (
            <CopyButton text={config.tokenAddress} title="Copy token address" />
          )}
        </Flex>

        <Label>Faucet</Label>
        <Flex css={{ alignItems: 'center', gap: '$xxs', minWidth: 0 }}>
          <Value>{shortFaucetAddress}</Value>
          {faucetAddress && (
            <CopyButton text={faucetAddress} title="Copy faucet address" />
          )}
        </Flex>
      </DetailGrid>

      <Button
        fullWidth
        color="secondary"
        disabled={!faucetAddress || !isConnected || isBusy}
        onClick={handleClaim}
      >
        {buttonLabel}
      </Button>

      {isConfirmed && (
        <StatusText tone="success">
          Claim confirmed. Your test OBOL is on Base Sepolia.
        </StatusText>
      )}
      {errorMessage && <StatusText tone="warning">{errorMessage}</StatusText>}
      {txUrl && (
        <TxLink href={txUrl} target="_blank" rel="noreferrer">
          View transaction on BaseScan
        </TxLink>
      )}
    </FaucetCard>
  );
}
