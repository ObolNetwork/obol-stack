'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import type { AgentInstance } from '@/types/agent';

export interface AgentChatMessage {
  role: 'user' | 'assistant' | 'system';
  content: string;
}

interface ChatOptions {
  temperature?: number;
  max_tokens?: number;
  top_p?: number;
}

export function useAgentChat(instance: AgentInstance | null) {
  const [messages, setMessages] = useState<AgentChatMessage[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sessionId, setSessionId] = useState<string | null>(null);

  const instanceKey = useMemo(
    () => (instance ? `${instance.runtime}:${instance.id}` : ''),
    [instance]
  );

  useEffect(() => {
    setMessages([]);
    setError(null);
    setSessionId(null);
  }, [instanceKey]);

  const sendMessage = useCallback(
    async (content: string, options?: ChatOptions) => {
      if (!instance) {
        return;
      }

      setIsLoading(true);
      setError(null);

      const userMessage: AgentChatMessage = { role: 'user', content };
      const updatedMessages = [...messages, userMessage];
      setMessages(updatedMessages);

      try {
        const response = await fetch('/api/agents/chat', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            runtime: instance.runtime,
            instanceId: instance.id,
            messages: updatedMessages,
            stream: false,
            sessionId,
            ...options,
          }),
        });

        const nextSessionId = response.headers.get('X-Hermes-Session-Id');
        if (nextSessionId) {
          setSessionId(nextSessionId);
        }

        if (!response.ok) {
          const errorData = await response.json();
          throw new Error(errorData.error || 'Failed to send message');
        }

        const data = await response.json();
        const assistantMessage: AgentChatMessage = {
          role: 'assistant',
          content:
            data?.choices?.[0]?.message?.content ||
            'No response content returned.',
        };

        setMessages([...updatedMessages, assistantMessage]);
      } catch (err) {
        const errorMessage =
          err instanceof Error ? err.message : 'Unknown error';
        setError(errorMessage);
        console.error('[Agent Chat] Error:', err);
      } finally {
        setIsLoading(false);
      }
    },
    [instance, messages, sessionId]
  );

  const clearMessages = useCallback(() => {
    setMessages([]);
    setError(null);
    setSessionId(null);
  }, []);

  return {
    messages,
    isLoading,
    error,
    sendMessage,
    clearMessages,
  };
}
