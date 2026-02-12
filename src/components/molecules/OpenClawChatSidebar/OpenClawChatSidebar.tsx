'use client';

import React, { useRef, useEffect, useState, useCallback } from 'react';
import { Box, Flex, Text, Image, styled } from '@obolnetwork/obol-ui';
import { Close } from '@/icons';
import { useOpenClawChat } from '../../../hooks/useOpenClawChat';
import { useOpenClawStatus } from '../../../hooks/useOpenClaw';
import { StatusIndicator } from '../../atoms/StatusIndicator';
import type { OpenClawInstance } from '@/types/openclaw';

interface OpenClawChatSidebarProps {
  instance: OpenClawInstance | null;
  isSidebarOpen: boolean;
  onClose: () => void;
  onSidebarToggle: (isOpen: boolean) => void;
}

// Sidebar container with slide animation
const SidebarOverlay = styled(Box, {
  position: 'fixed',
  top: 0,
  right: 0,
  bottom: 0,
  width: '28rem',
  zIndex: 30,
  display: 'flex',
  flexDirection: 'column',
  backgroundColor: '#091011',
  borderLeft: '1px solid #2D4D53',
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

// Floating toggle button
const ToggleButton = styled(Box, {
  position: 'fixed',
  bottom: '32px',
  right: '32px',
  zIndex: 31,
  cursor: 'pointer',

  '@media (max-width: 1100px)': {
    right: '16px',
    bottom: '16px',
  },
});

const ToggleImage = styled(Image, {
  width: '80px',
  height: '80px',
  objectFit: 'contain',
});

// Header
const HeaderContainer = styled(Flex, {
  width: '100%',
  height: '$4xl',
  padding: '$xxs $sm',
  justifyContent: 'space-between',
  alignItems: 'center',
  borderBottom: '1px solid #2D4D53',
  flexShrink: 0,
});

const HeaderTitle = styled(Text, {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '#e1e9eb',
});

const CloseButton = styled(Box, {
  width: '$lg',
  height: '$lg',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  cursor: 'pointer',
  color: '#b8c5c7',
  transition: 'color 0.2s ease',
  '&:hover': {
    color: '#ffffff',
  },
});

// Messages area
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
        backgroundColor: '#182d32',
        color: '#e1e9eb',
        borderRadius: '8px 0 8px 8px',
      },
      assistant: {
        alignSelf: 'flex-start',
        backgroundColor: '#222829',
        color: '#e1e9eb',
        borderRadius: '0 8px 8px 8px',
      },
    },
  },
});

const InitialMessage = styled(Text, {
  color: '#b8c5c7',
  fontSize: '$3',
  lineHeight: '24px',
  padding: '$sm',
});

const ErrorMessage = styled(Text, {
  color: '#ff6b6b',
  fontSize: '$2',
  padding: '$xs $sm',
});

// Input area
const InputContainer = styled(Flex, {
  padding: '$sm',
  borderTop: '1px solid #2D4D53',
  gap: '$xs',
  alignItems: 'flex-end',
  flexShrink: 0,
});

const ChatInput = styled('textarea', {
  flex: 1,
  minHeight: '24px',
  maxHeight: '120px',
  padding: '12px 16px',
  backgroundColor: '#111f22',
  color: '#e1e9eb',
  border: 'none',
  borderRadius: '8px',
  fontFamily: 'DM Sans',
  fontSize: '16px',
  lineHeight: '24px',
  resize: 'none',
  outline: 'none',

  '&::placeholder': {
    color: '#6b7a7d',
  },
});

const SendButton = styled('button', {
  padding: '8px 16px',
  backgroundColor: '#2FE4AB',
  color: '#091011',
  border: 'none',
  borderRadius: '8px',
  fontFamily: 'DM Sans',
  fontSize: '14px',
  fontWeight: 600,
  cursor: 'pointer',
  transition: 'opacity 0.2s ease',

  '&:hover': {
    opacity: 0.9,
  },

  '&:disabled': {
    opacity: 0.5,
    cursor: 'not-allowed',
  },
});

export function OpenClawChatSidebar({
  instance,
  isSidebarOpen,
  onClose,
  onSidebarToggle,
}: OpenClawChatSidebarProps) {
  const { status: instanceStatus, loading: statusLoading } = useOpenClawStatus(
    instance?.id ?? ''
  );
  const { messages, isLoading, error, sendMessage, clearMessages } =
    useOpenClawChat(instance?.id ?? '');
  const [input, setInput] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);

  // // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  const handleClose = useCallback(() => {
    onSidebarToggle(false);
    clearMessages();
    onClose();
  }, [onClose]);

  const handleSend = useCallback(() => {
    const trimmed = input.trim();
    if (!trimmed || isLoading || !instance) return;
    setInput('');
    sendMessage(trimmed);
  }, [input, isLoading, instance, sendMessage]);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <>
      {/* Sidebar panel */}
      <SidebarOverlay isOpen={isSidebarOpen}>
        {/* Header */}
        <HeaderContainer>
          <Flex
            css={{ alignItems: 'center', gap: '$xs', justifyContent: 'center' }}
          >
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
            <HeaderTitle>
              {instance ? `openclaw-${instance.id}` : 'OpenClaw'}
            </HeaderTitle>
          </Flex>
          <CloseButton onClick={handleClose}>
            <Close size="s" />
          </CloseButton>
        </HeaderContainer>

        {/* Messages */}
        <MessagesContainer>
          {!instance && (
            <InitialMessage>
              Select an OpenClaw instance from the dashboard to start chatting.
            </InitialMessage>
          )}
          {instance && messages.length === 0 && (
            <InitialMessage>
              Chat with your OpenClaw instance &quot;{instance.id}&quot;. Ask
              anything!
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
          {error && <ErrorMessage>{error}</ErrorMessage>}
          <div ref={messagesEndRef} />
        </MessagesContainer>

        {/* Input */}
        <InputContainer>
          <ChatInput
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
          <SendButton
            onClick={handleSend}
            disabled={isLoading || !input.trim() || !instance}
          >
            Send
          </SendButton>
        </InputContainer>
      </SidebarOverlay>
    </>
  );
}
