'use client';

import {
  Box,
  Flex,
  Text,
  Button,
  TextField,
  styled,
  DialogTitle,
  ResponsiveDialog,
} from '@obolnetwork/obol-ui';
import { Button as BaseButton } from '@/components/atoms/Button';
import { tones } from '@/lib/ui/tones';
import { ChainSelector } from '../../atoms/ChainSelector';
import { SellFlowSteps } from '../SellFlowSteps';
import { useSellAgentModal } from './useSellAgentModal';
import type { SellPaymentToken } from '@/lib/services/sell-assets';

interface SellAgentModalProps {
  agentName: string;
  agentNamespace: string;
  defaultWallet?: string;
  onListed?: () => void;
  trigger: React.ReactNode;
}

const ModalBody = styled(Box, {
  padding: '$lg',
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
  flex: 1,
});

const FieldGroup = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xxxs',
});

const FieldLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontWeight: '$medium',
  letterSpacing: '0.03em',
  textTransform: 'uppercase',
});

const TokenToggle = styled(BaseButton, {
  variants: {
    active: {
      true: {
        background: tones.success.bg,
        borderColor: tones.success.border,
        color: tones.success.text,
        '&:hover:not(:disabled)': {
          background: tones.success.bg,
          borderColor: tones.success.border,
          color: tones.success.text,
        },
      },
    },
  },
});

const ModalFooter = styled(Flex, {
  padding: '$md $lg',
  borderTop: '1px solid $stroke',
  justifyContent: 'flex-end',
  gap: '$xs',
});

const ErrorMessage = styled(Text, {
  fontSize: '$2',
  color: '$test',
  padding: '$xxs $xs',
  backgroundColor: 'rgba(255, 107, 107, 0.08)',
  borderRadius: '$2',
  border: '1px solid rgba(255, 107, 107, 0.15)',
});

/** Gate a sub-agent (`obol agent new`) with x402 — ServiceOffer type=agent. */
export function SellAgentModal({
  agentName,
  agentNamespace,
  defaultWallet,
  onListed,
  trigger,
}: SellAgentModalProps) {
  const {
    open,
    setOpen,
    submitState,
    errorMsg,
    followUpSteps,
    chainValue,
    tokenValue,
    paymentError,
    disabledChainValues,
    isValid,
    register,
    selectPaymentToken,
    selectPaymentChain,
    handleSubmit,
    onSubmit,
    handleClose,
  } = useSellAgentModal(agentName, agentNamespace, defaultWallet, onListed);

  return (
    <ResponsiveDialog
      open={open}
      setOpen={setOpen}
      triggerElement={trigger as any}
    >
      <Flex css={{ paddingLeft: '$xs' }}>
        {/* Radix Title gives the dialog its accessible name. */}
        <DialogTitle
          css={{
            margin: 0,
            fontSize: '$5',
            fontWeight: '$semibold',
            color: '$light',
          }}
        >
          Sell agent
        </DialogTitle>
      </Flex>

      {submitState === 'success' ? (
        <ModalBody>
          <Text css={{ fontSize: '$2', color: '$body' }}>
            Now on sale — sub-agent{' '}
            <Text
              as="span"
              css={{ color: '$obolGreen', fontFamily: 'monospace' }}
            >
              {agentName}
            </Text>
            . Follow these steps:
          </Text>
          <SellFlowSteps steps={followUpSteps} />
          <Button
            color="primary"
            onClick={handleClose}
            css={{ alignSelf: 'flex-end' }}
          >
            Done
          </Button>
        </ModalBody>
      ) : (
        <form
          onSubmit={handleSubmit(onSubmit)}
          style={{ display: 'flex', flexDirection: 'column', flex: 1 }}
        >
          <ModalBody>
            <Text css={{ fontSize: '$2', color: '$body', lineHeight: 1.5 }}>
              Publish this sub-agent on the storefront. Buyers pay per request;
              the controller routes to the agent HTTP API.
            </Text>

            <FieldGroup>
              <FieldLabel>Service name</FieldLabel>
              <Text css={{ fontFamily: 'monospace', color: '$obolGreen' }}>
                {agentName}
              </Text>
              <Text css={{ fontSize: '$1', color: '$muted', marginTop: 4 }}>
                {`Matches the sub-agent name (obol sell agent ${agentName}).`}
              </Text>
            </FieldGroup>

            <FieldGroup>
              <FieldLabel>Payout wallet (pay-to)</FieldLabel>
              <TextField
                {...register('wallet', {
                  required: true,
                  validate: v => v.startsWith('0x') && v.length === 42,
                })}
              />
            </FieldGroup>

            <Flex css={{ gap: '$sm' }}>
              <FieldGroup css={{ flex: 1 }}>
                <FieldLabel>Token</FieldLabel>
                <Flex css={{ gap: '$xs' }}>
                  {(['USDC', 'OBOL'] as SellPaymentToken[]).map(token => (
                    <TokenToggle
                      key={token}
                      type="button"
                      variant="secondary"
                      size="sm"
                      pill
                      active={tokenValue === token}
                      onClick={() => selectPaymentToken(token)}
                    >
                      {token}
                    </TokenToggle>
                  ))}
                </Flex>
              </FieldGroup>
              <FieldGroup css={{ flex: 1 }}>
                <FieldLabel>Chain</FieldLabel>
                <ChainSelector
                  value={chainValue}
                  onChange={selectPaymentChain}
                  disabledValues={disabledChainValues}
                  disabledReason={`${tokenValue} is not configured on this chain.`}
                />
              </FieldGroup>
            </Flex>

            <FieldGroup>
              <FieldLabel>Price / request</FieldLabel>
              <TextField
                type="number"
                step="0.0001"
                min="0"
                {...register('price', {
                  required: true,
                  validate: v => parseFloat(v) > 0,
                })}
              />
            </FieldGroup>

            {(paymentError || submitState === 'error') && (
              <ErrorMessage>{paymentError ?? errorMsg}</ErrorMessage>
            )}
          </ModalBody>

          <ModalFooter>
            <Button color="secondary" type="button" onClick={handleClose}>
              Cancel
            </Button>
            <Button
              color="primary"
              type="submit"
              disabled={!isValid || submitState === 'submitting'}
            >
              {submitState === 'submitting'
                ? 'Putting on sale…'
                : 'Put on sale'}
            </Button>
          </ModalFooter>
        </form>
      )}
    </ResponsiveDialog>
  );
}
