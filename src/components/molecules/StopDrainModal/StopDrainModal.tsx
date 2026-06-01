'use client';

import { useState, type ReactNode } from 'react';
import {
  Box,
  Flex,
  Text,
  Button,
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
  const [submitState, setSubmitState] = useState<SubmitState>('idle');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const close = () => {
    setOpen(false);
    setSubmitState('idle');
    setErrorMsg(null);
  };

  const submit = async () => {
    setSubmitState('submitting');
    setErrorMsg(null);
    try {
      const response = await fetch('/api/sell/stop', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name,
          namespace,
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
          ? The listing is removed from public discovery immediately. Buyers
          with in-flight payments can finish during the drain window, then the
          route is torn down while the ServiceOffer record is retained for
          status and audit history.
        </Text>

        <ResourceLine>
          {namespace}/{name}
        </ResourceLine>

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
          color="primary"
          type="button"
          onClick={submit}
          disabled={submitState === 'submitting'}
        >
          {submitState === 'submitting' ? 'Draining…' : 'Stop & drain'}
        </Button>
      </ModalFooter>
    </ResponsiveDialog>
  );
}
