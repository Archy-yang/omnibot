import { defineStore } from 'pinia';
import { ref, computed, reactive } from 'vue';
import type { Message } from '../types/chat';
import { chatService } from '../services/chat';

export const useChatStore = defineStore(
  'chat',
  () => {
    // State
    const messages = ref<Message[]>([]);
    const isLoading = ref<boolean>(false);
    const sessionId = ref<string>('');

    // Getters
    const messageCount = computed<number>(() => messages.value.length);
    const lastMessage = computed<Message | null>(
      () => messages.value[messages.value.length - 1] || null
    );

    // Actions
    const addMessage = (message: Message): void => {
      messages.value.push(message);
    };

    const clearMessages = (): void => {
      messages.value = [];
    };

    const sendMessage = async (content: string): Promise<void> => {
      if (!content.trim()) {
        return;
      }

      // Add user message immediately
      const userMessage: Message = {
        id: Date.now(),
        role: 'user',
        content,
        created_at: new Date().toISOString(),
      };
      addMessage(userMessage);

      // Add placeholder assistant message for streaming
      const assistantMessage = reactive<Message>({
        id: Date.now() + 1,
        role: 'assistant',
        content: '',
        created_at: new Date().toISOString(),
        segments: [],
      });
      addMessage(assistantMessage);

      isLoading.value = true;
      try {
        await chatService.sendMessageStream(content, sessionId.value, {
          onChunk: (chunk: string) => {
            // content 始终累加（复制按钮 / 落库用纯文本）
            assistantMessage.content += chunk;
            // segments 按时序维护：末尾是 text 段就追加，否则新建一段 text
            const segs = assistantMessage.segments!;
            const last = segs[segs.length - 1];
            if (last && last.type === 'text') {
              last.content += chunk;
            } else {
              segs.push({ type: 'text', content: chunk });
            }
          },
          onToolCall: (event) => {
            // push 一个 tool 段，自然封口上一段文本；result 待 onToolResult 回填
            assistantMessage.segments!.push({
              type: 'tool',
              tool: event.tool,
              label: event.label,
              expanded: false,
            });
          },
          onToolResult: (event) => {
            // 从尾部找最后一个尚未回填 result 的同名 tool 段
            const segs = assistantMessage.segments!;
            for (let i = segs.length - 1; i >= 0; i--) {
              const seg = segs[i];
              if (seg.type === 'tool' && seg.result === undefined) {
                seg.result = event.result;
                break;
              }
            }
          },
          onDone: () => {
            isLoading.value = false;
          },
          onError: (err: Error) => {
            isLoading.value = false;
            // Remove placeholder on error
            messages.value = messages.value.filter((m) => m.id !== assistantMessage.id);
            throw err;
          },
        });
      } catch (error) {
        console.error('Failed to send message:', error);
        if (isLoading.value) {
          isLoading.value = false;
        }
        throw error;
      }
    };

    const loadHistory = async (): Promise<void> => {
      if (!sessionId.value) {
        return;
      }

      isLoading.value = true;
      try {
        const history = await chatService.getHistory(sessionId.value);
        messages.value = history;
      } catch (error) {
        console.error('Failed to load chat history:', error);
        throw error;
      } finally {
        isLoading.value = false;
      }
    };

    const setSessionId = (id: string): void => {
      sessionId.value = id;
    };

    return {
      // State
      messages,
      isLoading,
      sessionId,
      // Getters
      messageCount,
      lastMessage,
      // Actions
      sendMessage,
      loadHistory,
      addMessage,
      clearMessages,
      setSessionId,
    };
  },
  {
    persist: {
      key: 'chat-store',
      storage: localStorage,
      pick: ['sessionId', 'messages'],
    },
  }
);
