import { request } from '../utils/request';
import type { ApiResponse } from '../types/api';

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1';

/** 后台 Agent 任务(轮询用) */
export interface AgentTask {
  id: number;
  sub_agent_type: string;
  goal: string;
  status: string;
  reported: boolean;
}

/** 任务汇报 SSE 流回调(与 chat.sendMessageStream 同款事件) */
export interface ReportStreamCallbacks {
  onChunk: (content: string) => void;
  onToolCall?: (event: { tool: string; label: string }) => void;
  onToolResult?: (event: { tool: string; result: string }) => void;
  onFinal?: (content: string) => void;
  onThought?: (content: string) => void;
  onDone: (fullContent: string) => void;
  onError: (error: Error) => void;
}

export const agentTaskService = {
  /**
   * 轮询未汇报的已完成任务(08 §4.7)。
   * 返回任务列表;空数组表示无待汇报。
   */
  async listUnreported(): Promise<AgentTask[]> {
    try {
      const response = await request.get<ApiResponse<{ tasks: AgentTask[] }>>(
        '/agent/tasks',
        { params: { status: 'completed_unreported' } },
      );
      return response.data.data.tasks;
    } catch (error) {
      console.error('Failed to list unreported agent tasks:', error);
      return [];
    }
  },

  /**
   * 触发主 Agent 流式汇报指定任务(08 §4.5)。
   * SSE 协议与 sendMessageStream 一致(token/thought/final/tool_call/tool_result/done)。
   * 汇报不落 messages 表,前端把汇报作为流式助手消息渲染。
   */
  async reportTask(taskId: number, callbacks: ReportStreamCallbacks): Promise<void> {
    const { onChunk, onToolCall, onToolResult, onFinal, onThought, onDone, onError } = callbacks;

    try {
      const token = localStorage.getItem('token');
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };
      if (token) {
        headers.Authorization = `Bearer ${token}`;
      }

      const response = await fetch(`${BASE_URL}/agent/tasks/${taskId}/report`, {
        method: 'POST',
        headers,
      });

      if (response.status === 401) {
        localStorage.removeItem('token');
        window.location.href = '/login';
        return;
      }
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
            if (currentEvent === 'error' || parsed.error) {
              onError(new Error(parsed.error || 'stream error'));
              return;
            }
            if (currentEvent === 'tool_call') {
              onToolCall?.(parsed);
              currentEvent = 'message';
              continue;
            }
            if (currentEvent === 'tool_result') {
              onToolResult?.(parsed);
              currentEvent = 'message';
              continue;
            }
            if (currentEvent === 'final') {
              onFinal?.(parsed.content);
              currentEvent = 'message';
              continue;
            }
            if (currentEvent === 'thought') {
              onThought?.(parsed.content);
              currentEvent = 'message';
              continue;
            }
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
};

export default agentTaskService;
