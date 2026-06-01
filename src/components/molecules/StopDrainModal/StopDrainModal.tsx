'use client';

import { useState, type ReactNode } from 'react';
import {
  Box,
  Flex,
  Text,
  Button,
  TextField,
  styled,
  ResponsiveDialog,
} from '@obolnetwork/obol-ui';
import { SectionTitle } from '../../atoms/SectionTitle';

interface StopDrainModalProps {
  trigger: ReactNode;
  name: string;
  namespace: string;
  onDrained?: (result: { drainEndsAt: string; grace: string }) => void;
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

const ResourceLine = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  fontFamily: 'monospace',
});

type SubmitState = 'idle' | 'submitting' | 'error';

export function StopDrainModal({
  trigger,
  name,
  namespace,
  onDrained,
}: StopDrainModalProps) {
  const [open, setOpen] = useState(false);
  const [grace, setGrace] = useState('1h');
  const [submitState, setSubmitState] = useState<SubmitState>('idle');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const close = () => {
    setOpen(false);
    setSubmitState('idle');
    setErrorMsg(null);
    setGrace('1h');
  };

  const submit = async (force: boolean) => {
    setSubmitState('submitting');
    setErrorMsg(null);
    try {
      const response = await fetch('/api/sell/stop', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name,
          namespace,
          ...(force ? { force: true } : { grace }),
        }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data?.error || `Drain failed (${response.status})`);
      }
      onDrained?.({
        drainEndsAt: data.drainEndsAt,
        grace: data.grace,
      });
      close();
    } catch (err) {
      setSubmitState('error');
      setErrorMsg(err instanceof Error ? err.message : 'unknown error');
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      setOpen={setOpen}
      triggerElement={trigger as any}
    >
      <Flex css={{ paddingLeft: '$xs' }}>
        <SectionTitle>Stop &amp; drain</SectionTitle>
      </Flex>

      <ModalBody>
        <Text css={{ fontSize: '$2', color: '$body', lineHeight: 1.5 }}>
          Stop and drain{' '}
          <Text
            as="span"
            css={{ color: '$obolGreen', fontFamily: 'monospace' }}
          >
            {name}
          </Text>
          ? Buyers with in-flight payments will be served for{' '}
          <Text as="span" css={{ color: '$light', fontFamily: 'monospace' }}>
            {grace}
          </Text>
          . The listing is removed from public discovery immediately. After the
          grace period the route is torn down while the ServiceOffer record is
          retained for status and audit history.
        </Text>

        <ResourceLine>
          {namespace}/{name}
        </ResourceLine>

        <FieldGroup>
          <FieldLabel>Grace period (Go duration)</FieldLabel>
          <TextField
            value={grace}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
              setGrace(e.target.value)
            }
            placeholder="1h"
            disabled={submitState === 'submitting'}
          />
          <Text css={{ fontSize: '$1', color: '$muted' }}>
            Examples: 1h, 30m, 5m. Default 1h. Protects ERC-8004 reputation
            against abrupt teardowns that look like crashes.
          </Text>
        </FieldGroup>

        {submitState === 'error' && errorMsg && (
          <ErrorMessage>{errorMsg}</ErrorMessage>
        )}
      </ModalBody>

      <ModalFooter>
        <Button
          color="secondary"
          type="button"
          onClick={close}
          disabled={submitState === 'submitting'}
        >
          Cancel
        </Button>
        <Button
          color="secondary"
          type="button"
          onClick={() => submit(true)}
          disabled={submitState === 'submitting'}
          title="Skip the grace period — buyers with pending requests will see errors"
        >
          Force stop now
        </Button>
        <Button
          color="primary"
          type="button"
          onClick={() => submit(false)}
          disabled={submitState === 'submitting' || grace.trim() === ''}
        >
          {submitState === 'submitting' ? 'Draining…' : 'Stop & drain'}
        </Button>
      </ModalFooter>
    </ResponsiveDialog>
  );
}
