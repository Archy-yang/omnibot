import { request } from '../utils/request';
import type { Message } from '../types/chat';
import type { AgentStepEvent, ApiResponse, GetHistoryResponse, PaginationParams, SendMessageRequest } from '../types/api';

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1';

interface StreamCallbacks {
  onChunk: (content: string) => void;
  onDone: (fullContent: string) => void;
  onError: (error: Error) => void;
  onAgentStep?: (step: AgentStepEvent) => void;
}

export const chatService = {
  async sendMessage(content: string, sessionId: string): Promise<Message> {
    try {
      const requestData: SendMessageRequest = {
        content,
        session_id: sessionId,
      };

      const response = await request.post<ApiResponse<Message>>('/chat/messages', requestData);
      return response.data.data;
    } catch (error) {
      console.error('Failed to send message:', error);
      throw error;
    }
  },

  async sendMessageStream(
    content: string,
    sessionId: string,
    isAgentMode: boolean = false,
    callbacks: StreamCallbacks
  ): Promise<void> {
    const { onChunk, onDone, onError, onAgentStep } = callbacks;

    try {
      const endpoint = isAgentMode ? '/chat/messages/agent/stream' : '/chat/messages/stream';
      const response = await fetch(`${BASE_URL}${endpoint}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content, session_id: sessionId }),
      });

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }

      const reader = response.body?.getReader();
      if (!reader) {
        throw new Error('ReadableStream not supported');
      }

      const decoder = new TextDecoder();
      let fullContent = '';
      let buffer = '';
      let currentEvent = 'message';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() || '';

        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed) continue;

          if (trimmed.startsWith('event: ')) {
            currentEvent = trimmed.slice(7);
            continue;
          }
          if (!trimmed.startsWith('data: ')) continue;

          const data = trimmed.slice(6);
          if (data === '[DONE]') {
            onDone(fullContent);
            return;
          }

          try {
            const parsed = JSON.parse(data);
            if (parsed.error) {
              onError(new Error(parsed.error));
              return;
            }
            if (currentEvent === 'agent_step') {
              onAgentStep?.(parsed as AgentStepEvent);
              currentEvent = 'message';
              continue;
            }
            if (parsed.content !== undefined) {
              fullContent += parsed.content;
              onChunk(parsed.content);
            }
          } catch {
            // skip unparseable lines
          }
        }
      }

      onDone(fullContent);
    } catch (err) {
      onError(err instanceof Error ? err : new Error(String(err)));
    }
  },

  async getHistory(
    sessionId: string,
    params?: PaginationParams
  ): Promise<Message[]> {
    try {
      const response = await request.get<ApiResponse<GetHistoryResponse>>('/chat/messages', {
        params: {
          session_id: sessionId,
          limit: params?.limit || 50,
          before: params?.before,
        },
      });
      return response.data.data.messages;
    } catch (error) {
      console.error('Failed to get chat history:', error);
      throw error;
    }
  },
};

export default chatService;
