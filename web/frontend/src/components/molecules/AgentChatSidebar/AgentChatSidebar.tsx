'use client';

import React, { useRef, useEffect, useState, useCallback } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Button } from '@/components/atoms/Button';
import { Close } from '@/icons';
import { useAgentChat } from '../../../hooks/useAgentChat';
import { useAgentStatus } from '../../../hooks/useAgents';
import { StatusIndicator } from '../../atoms/StatusIndicator';
import type { AgentInstance } from '@/types/agent';
import { inputFocus, palette } from '@/lib/ui/theme';
import { tones } from '@/lib/ui/tones';

interface AgentChatSidebarProps {
  instance: AgentInstance | null;
  isSidebarOpen: boolean;
  onClose: () => void;
  onSidebarToggle: (isOpen: boolean) => void;
}

const MIN_WIDTH = 280;
const DEFAULT_WIDTH = 448;

const SidebarOverlay = styled(Box, {
  position: 'fixed',
  top: 0,
  right: 0,
  bottom: 0,
  zIndex: 30,
  display: 'flex',
  flexDirection: 'column',
  backgroundColor: '$bg01',
  borderLeft: '1px solid $bg05',
  transition: 'transform 0.3s ease-in-out',

  variants: {
    isOpen: {
      true: {
        transform: 'translateX(0)',
      },
      false: {
        transform: 'translateX(100%)',
      },
    },
  },
});

const ResizeHandle = styled(Box, {
  position: 'absolute',
  top: 0,
  left: 0,
  width: '4px',
  height: '100%',
  cursor: 'col-resize',
  zIndex: 32,
  transition: 'background-color 0.15s ease',
  '&:hover': {
    backgroundColor: tones.success.border,
  },
  '&:active': {
    backgroundColor: '$obolGreen',
  },
});

const HeaderContainer = styled(Flex, {
  width: '100%',
  height: '$4xl',
  padding: '$xxs $sm',
  justifyContent: 'space-between',
  alignItems: 'center',
  borderBottom: '1px solid $bg05',
  flexShrink: 0,
});

const HeaderTitle = styled(Text, {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '$textLight',
});

const RuntimeLabel = styled(Text, {
  fontSize: '$1',
  color: '$textMiddle',
  textTransform: 'uppercase',
  letterSpacing: '0.08em',
});

const MessagesContainer = styled(Box, {
  flex: 1,
  overflowY: 'auto',
  padding: '$md',
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

const MessageBubble = styled(Box, {
  maxWidth: '85%',
  padding: '12px 16px',
  fontFamily: 'DM Sans',
  fontSize: '16px',
  fontWeight: 400,
  lineHeight: '24px',
  wordBreak: 'break-word',
  whiteSpace: 'pre-wrap',

  variants: {
    role: {
      user: {
        alignSelf: 'flex-end',
        backgroundColor: '$bg03',
        color: '$textLight',
        borderRadius: '8px 0 8px 8px',
      },
      assistant: {
        alignSelf: 'flex-start',
        backgroundColor: palette.chatBubble,
        color: '$textLight',
        borderRadius: '0 8px 8px 8px',
      },
    },
  },
});

const InitialMessage = styled(Text, {
  color: '$textMiddle',
  fontSize: '$3',
  lineHeight: '24px',
  padding: '$sm',
});

const ErrorMessage = styled(Text, {
  color: '$test',
  fontSize: '$2',
  padding: '$xs $sm',
});

const InputContainer = styled(Flex, {
  padding: '$sm',
  borderTop: '1px solid $bg05',
  gap: '$xs',
  alignItems: 'flex-end',
  flexShrink: 0,
});

const ChatInput = styled('textarea', {
  flex: 1,
  minHeight: '24px',
  maxHeight: '120px',
  padding: '12px 16px',
  backgroundColor: '$bg02',
  color: '$textLight',
  border: 'none',
  borderRadius: '8px',
  fontFamily: 'DM Sans',
  fontSize: '16px',
  lineHeight: '24px',
  resize: 'none',
  ...inputFocus,

  '&::placeholder': {
    color: '$grayMedium',
  },
});

function displayInstanceName(instance: AgentInstance): string {
  if (instance.runtime === 'hermes' && instance.id === 'obol-agent') {
    return 'Stack agent';
  }
  return instance.id;
}

function runtimeLabel(instance: AgentInstance): string {
  return instance.runtime === 'hermes' ? 'Hermes' : 'Agent';
}

export function AgentChatSidebar({
  instance,
  isSidebarOpen,
  onClose,
  onSidebarToggle,
}: AgentChatSidebarProps) {
  const { status: instanceStatus, loading: statusLoading } =
    useAgentStatus(instance);
  const { messages, isLoading, error, sendMessage, clearMessages } =
    useAgentChat(instance);
  const [input, setInput] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const [sidebarWidth, setSidebarWidth] = useState(DEFAULT_WIDTH);
  const isDragging = useRef(false);
  const dragStartX = useRef(0);
  const dragStartWidth = useRef(DEFAULT_WIDTH);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const handleResizeMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      isDragging.current = true;
      dragStartX.current = e.clientX;
      dragStartWidth.current = sidebarWidth;
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    },
    [sidebarWidth]
  );

  useEffect(() => {
    const onMouseMove = (e: MouseEvent) => {
      if (!isDragging.current) return;
      const delta = dragStartX.current - e.clientX;
      const newWidth = Math.min(
        window.innerWidth,
        Math.max(MIN_WIDTH, dragStartWidth.current + delta)
      );
      setSidebarWidth(newWidth);
    };

    const onMouseUp = () => {
      if (!isDragging.current) return;
      isDragging.current = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };

    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', onMouseUp);
    return () => {
      document.removeEventListener('mousemove', onMouseMove);
      document.removeEventListener('mouseup', onMouseUp);
    };
  }, []);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  const handleClose = useCallback(() => {
    onSidebarToggle(false);
    clearMessages();
    onClose();
  }, [clearMessages, onClose, onSidebarToggle]);

  // Move focus into the panel when it opens; Escape closes it.
  const handleCloseRef = useRef(handleClose);
  useEffect(() => {
    handleCloseRef.current = handleClose;
  }, [handleClose]);
  useEffect(() => {
    if (!isSidebarOpen) return;
    inputRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') handleCloseRef.current();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [isSidebarOpen]);

  const handleSend = useCallback(() => {
    const trimmed = input.trim();
    if (!trimmed || isLoading || !instance) return;
    setInput('');
    sendMessage(trimmed);
  }, [input, instance, isLoading, sendMessage]);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <SidebarOverlay
      as="aside"
      aria-labelledby="agent-chat-title"
      aria-hidden={!isSidebarOpen}
      // Off-screen panel must not be reachable by Tab or screen readers.
      inert={!isSidebarOpen}
      isOpen={isSidebarOpen}
      css={{ width: sidebarWidth, maxWidth: '100vw' }}
    >
      <ResizeHandle aria-hidden="true" onMouseDown={handleResizeMouseDown} />

      <HeaderContainer>
        <Flex
          css={{
            alignItems: 'center',
            gap: '$xs',
            justifyContent: 'center',
            flexDirection: 'column',
          }}
        >
          <Flex css={{ alignItems: 'center', gap: '$xs' }}>
            <StatusIndicator
              status={
                statusLoading
                  ? 'unsynced'
                  : instanceStatus?.healthy
                    ? 'synced'
                    : 'unsynced'
              }
              size="sm"
            />
            <HeaderTitle as="h2" id="agent-chat-title">
              {instance ? displayInstanceName(instance) : 'AI Agent'}
            </HeaderTitle>
          </Flex>
          {instance && <RuntimeLabel>{runtimeLabel(instance)}</RuntimeLabel>}
        </Flex>
        <Button
          type="button"
          variant="bare"
          size="sm"
          iconOnly
          aria-label="Close chat"
          onClick={handleClose}
        >
          <Close size="s" />
        </Button>
      </HeaderContainer>

      <MessagesContainer
        role="log"
        aria-live="polite"
        aria-label="Chat messages"
      >
        {!instance && (
          <InitialMessage>
            Select an agent from the dashboard to start chatting.
          </InitialMessage>
        )}
        {instance && messages.length === 0 && (
          <InitialMessage>
            Chat with {runtimeLabel(instance)} instance &quot;
            {displayInstanceName(instance)}&quot;. Ask anything!
          </InitialMessage>
        )}
        {messages.map((msg, i) => (
          <MessageBubble
            key={i}
            role={msg.role === 'user' ? 'user' : 'assistant'}
          >
            {msg.content}
          </MessageBubble>
        ))}
        {isLoading && (
          <MessageBubble role="assistant">Thinking...</MessageBubble>
        )}
        {error && <ErrorMessage role="alert">{error}</ErrorMessage>}
        <div ref={messagesEndRef} />
      </MessagesContainer>

      <InputContainer>
        <ChatInput
          ref={inputRef}
          aria-label="Message"
          value={input}
          onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) =>
            setInput(e.target.value)
          }
          onKeyDown={handleKeyDown}
          placeholder={
            instance ? 'Type a message...' : 'Select an instance first...'
          }
          rows={1}
          disabled={!instance}
        />
        <Button
          type="button"
          variant="primary"
          size="md"
          onClick={handleSend}
          disabled={isLoading || !input.trim() || !instance}
        >
          Send
        </Button>
      </InputContainer>
    </SidebarOverlay>
  );
}
