'use client';

import React, { useRef, useEffect, useState, useCallback } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Close } from '@/icons';
import { useAgentChat } from '../../../hooks/useAgentChat';
import { useAgentStatus } from '../../../hooks/useAgents';
import { StatusIndicator } from '../../atoms/StatusIndicator';
import type { AgentInstance } from '@/types/agent';

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
    backgroundColor: '#2FE4AB44',
  },
  '&:active': {
    backgroundColor: '#2FE4AB88',
  },
});

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

const RuntimeLabel = styled(Text, {
  fontSize: '$1',
  color: '#b8c5c7',
  textTransform: 'uppercase',
  letterSpacing: '0.08em',
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

function displayInstanceName(instance: AgentInstance): string {
  if (instance.runtime === 'hermes' && instance.id === 'obol-agent') {
    return 'Obol Agent';
  }
  if (instance.runtime === 'openclaw' && instance.id === 'default') {
    return 'OpenClaw';
  }
  return instance.id;
}

function runtimeLabel(instance: AgentInstance): string {
  return instance.runtime === 'hermes' ? 'Hermes' : 'OpenClaw';
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
    <SidebarOverlay isOpen={isSidebarOpen} css={{ width: sidebarWidth }}>
      <ResizeHandle onMouseDown={handleResizeMouseDown} />

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
            <HeaderTitle>
              {instance ? displayInstanceName(instance) : 'AI Agent'}
            </HeaderTitle>
          </Flex>
          {instance && <RuntimeLabel>{runtimeLabel(instance)}</RuntimeLabel>}
        </Flex>
        <CloseButton onClick={handleClose}>
          <Close size="s" />
        </CloseButton>
      </HeaderContainer>

      <MessagesContainer>
        {!instance && (
          <InitialMessage>
            Select a Hermes or OpenClaw instance from the dashboard to start
            chatting.
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
        {error && <ErrorMessage>{error}</ErrorMessage>}
        <div ref={messagesEndRef} />
      </MessagesContainer>

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
  );
}
