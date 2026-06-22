import { request } from '../utils/request';
import type { Message } from '../types/chat';
import type { ApiResponse, GetHistoryResponse, PaginationParams, SendMessageRequest, ToolCallEvent, ToolResultEvent } from '../types/api';

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1';

interface StreamCallbacks {
  onChunk: (content: string) => void;
  onToolCall?: (event: ToolCallEvent) => void;
  onToolResult?: (event: ToolResultEvent) => void;
  onDone: (fullContent: string) => void;
  onError: (error: Error) => void;
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

  /**
   * 默认全 Agent 模式的流式对话（v1.5.2）。
   *
   * 后端协议：
   *   event: token       data: {"content": "..."}    -- LLM token 增量
   *   event: tool_call   data: {"tool": "...", "label": "..."}  -- 工具调用
   *   event: tool_result data: {"tool": "...", "result": "..."} -- 工具结果（错误已脱敏）
   *   event: error       data: {"error": "..."}      -- 错误
   *   data: [DONE]                                    -- 完成
   *
   * 简单提问（无工具）只会产生 token + [DONE]，体验等同 v1.5.1 之前的「普通流式」。
   * 工具调用问题会先收到 tool_call 事件，前端可立即渲染状态条。
   */
  async sendMessageStream(
    content: string,
    sessionId: string,
    callbacks: StreamCallbacks
  ): Promise<void> {
    const { onChunk, onToolCall, onToolResult, onDone, onError } = callbacks;

    try {
      const response = await fetch(`${BASE_URL}/chat/messages/agent/stream`, {
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
      // SSE 事件类型按 OpenAI 同款规则：单条事件由 event:/data: 多行组成、用空行分隔。
      // 当前事件类型保持到下一次 event: 行覆盖；data: [DONE] 视为终止信号。
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
            if (currentEvent === 'error' || parsed.error) {
              onError(new Error(parsed.error || 'stream error'));
              return;
            }
            if (currentEvent === 'tool_call') {
              onToolCall?.(parsed as ToolCallEvent);
              currentEvent = 'message';
              continue;
            }
            if (currentEvent === 'tool_result') {
              onToolResult?.(parsed as ToolResultEvent);
              currentEvent = 'message';
              continue;
            }
            // 默认事件或 event: token 都按 token 处理（后端 [DONE] 之外的所有 data 都带 content 字段）
            if (parsed.content !== undefined) {
              fullContent += parsed.content;
              onChunk(parsed.content);
            }
            currentEvent = 'message';
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
