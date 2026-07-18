import { defineStore } from 'pinia';
import { ref, computed, reactive } from 'vue';
import type { Message } from '../types/chat';
import { chatService } from '../services/chat';

// v2.1: 身份由后端 JWT 中间件解析,前端不再维护 sessionId
export const useChatStore = defineStore(
  'chat',
  () => {
    // State
    const messages = ref<Message[]>([]);
    const isLoading = ref<boolean>(false);

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
        streaming: true, // 思考模式:流式中思考块展开实时显示过程,onDone 收起
      });
      addMessage(assistantMessage);

      isLoading.value = true;
      try {
        await chatService.sendMessageStream(content, {
          onChunk: (chunk: string) => {
            // 方案5:token 默认进 final 段(乐观当回复,主气泡实时显示)。
            // 末尾是 text 段且非 thought 才追加;末尾是 thought 段(已被思考轮封口)则新建 final 段。
            // 思考轮末的 onThought 会把当前 final 段改标 thought(迁移到思考块)。
            assistantMessage.content += chunk;
            const segs = assistantMessage.segments!;
            const last = segs[segs.length - 1];
            if (last && last.type === 'text' && last.role !== 'thought') {
              last.content += chunk;
            } else {
              segs.push({ type: 'text', role: 'final', content: chunk });
            }
          },
          onToolCall: (event) => {
            // push 一个 tool 段,自然封口上一段文本;result 待 onToolResult 回填
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
          onFinal: (content: string) => {
            // C5:回复轮标记。当前 final 段(已是 final)确认,content 修正为最终回复。
            const segs = assistantMessage.segments!;
            for (let i = segs.length - 1; i >= 0; i--) {
              const seg = segs[i];
              if (seg.type === 'text') {
                seg.role = 'final';
                break;
              }
            }
            assistantMessage.content = content;
          },
          onThought: (_content: string) => {
            // 方案5:思考轮标记。把最后一个 text 段改标 role=thought--该段从主气泡
            // 迁移到思考块(onChunk 的封口逻辑保证下轮 token 新建 final 段)。
            const segs = assistantMessage.segments!;
            for (let i = segs.length - 1; i >= 0; i--) {
              const seg = segs[i];
              if (seg.type === 'text') {
                seg.role = 'thought';
                break;
              }
            }
          },
          onDone: () => {
            // 流式结束。若未收到 onFinal(异常兜底),把最后一个 text 段标 final +
            // content 取它,保证总有最终回复展示。
            const segs = assistantMessage.segments;
            if (segs && segs.length > 0) {
              let hasFinal = segs.some((s) => s.type === 'text' && s.role === 'final');
              if (!hasFinal) {
                for (let i = segs.length - 1; i >= 0; i--) {
                  const seg = segs[i];
                  if (seg.type === 'text') {
                    seg.role = 'final';
                    assistantMessage.content = seg.content;
                    break;
                  }
                }
              }
            }
            assistantMessage.streaming = false;
            isLoading.value = false;
          },
          onError: (err: Error) => {
            assistantMessage.streaming = false;
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
      isLoading.value = true;
      try {
        const history = await chatService.getHistory();
        messages.value = history;
      } catch (error) {
        console.error('Failed to load chat history:', error);
        throw error;
      } finally {
        isLoading.value = false;
      }
    };

    return {
      // State
      messages,
      isLoading,
      // Getters
      messageCount,
      lastMessage,
      // Actions
      sendMessage,
      loadHistory,
      addMessage,
      clearMessages,
    };
  },
  {
    persist: {
      key: 'chat-store',
      storage: localStorage,
      pick: ['messages'],
    },
  }
);
