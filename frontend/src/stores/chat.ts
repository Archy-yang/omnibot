import { defineStore } from 'pinia';
import { ref, computed, reactive } from 'vue';
import type { Message } from '../types/chat';
import { chatService } from '../services/chat';
import { agentTaskService } from '../services/agentTask';

// v2.1: 身份由后端 JWT 中间件解析,前端不再维护 sessionId
export const useChatStore = defineStore(
  'chat',
  () => {
    // State
    const messages = ref<Message[]>([]);
    const isLoading = ref<boolean>(false);
    // 后台 Agent 任务轮询(08 §4.5):定时查未汇报任务,有则触发主 Agent 流式汇报
    let pollingTimer: ReturnType<typeof setInterval> | null = null;
    const isReporting = ref<boolean>(false); // 正在汇报某任务时暂停轮询避免并发

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

    // renderReport 把一个后台任务的汇报作为流式助手消息渲染(复用 sendMessage 的 segments 逻辑)。
    // 汇报消息不进 messages 表(后端不落库),仅前端展示。
    const renderReport = async (taskId: number): Promise<void> => {
      const reportMessage = reactive<Message>({
        id: Date.now(),
        role: 'assistant',
        content: '',
        created_at: new Date().toISOString(),
        segments: [],
        streaming: true,
      });
      addMessage(reportMessage);

      try {
        await agentTaskService.reportTask(taskId, {
          onChunk: (chunk: string) => {
            reportMessage.content += chunk;
            const segs = reportMessage.segments!;
            const last = segs[segs.length - 1];
            if (last && last.type === 'text' && last.role !== 'thought') {
              last.content += chunk;
            } else {
              segs.push({ type: 'text', role: 'final', content: chunk });
            }
          },
          onToolCall: (event) => {
            reportMessage.segments!.push({
              type: 'tool', tool: event.tool, label: event.label, expanded: false,
            });
          },
          onToolResult: (event) => {
            const segs = reportMessage.segments!;
            for (let i = segs.length - 1; i >= 0; i--) {
              const seg = segs[i];
              if (seg.type === 'tool' && seg.result === undefined) {
                seg.result = event.result;
                break;
              }
            }
          },
          onFinal: (content: string) => {
            const segs = reportMessage.segments!;
            for (let i = segs.length - 1; i >= 0; i--) {
              const seg = segs[i];
              if (seg.type === 'text') { seg.role = 'final'; break; }
            }
            reportMessage.content = content;
          },
          onThought: (_content: string) => {
            const segs = reportMessage.segments!;
            for (let i = segs.length - 1; i >= 0; i--) {
              const seg = segs[i];
              if (seg.type === 'text') { seg.role = 'thought'; break; }
            }
          },
          onDone: () => {
            const segs = reportMessage.segments;
            if (segs && segs.length > 0) {
              let hasFinal = segs.some((s) => s.type === 'text' && s.role === 'final');
              if (!hasFinal) {
                for (let i = segs.length - 1; i >= 0; i--) {
                  const seg = segs[i];
                  if (seg.type === 'text') { seg.role = 'final'; reportMessage.content = seg.content; break; }
                }
              }
            }
            reportMessage.streaming = false;
          },
          onError: (err: Error) => {
            reportMessage.streaming = false;
            console.error('Failed to render report:', err);
            // 汇报失败:移除占位消息(后端仍会标记 reported,下次不重试)
            messages.value = messages.value.filter((m) => m.id !== reportMessage.id);
          },
        });
      } catch (err) {
        reportMessage.streaming = false;
        console.error('Report stream failed:', err);
        messages.value = messages.value.filter((m) => m.id !== reportMessage.id);
      }
    };

    // pollOnce 查一次未汇报任务,有则依次汇报。
    const pollOnce = async (): Promise<void> => {
      if (isReporting.value) return; // 正在汇报时跳过,避免并发
      const tasks = await agentTaskService.listUnreported();
      if (tasks.length === 0) return;
      isReporting.value = true;
      try {
        for (const task of tasks) {
          await renderReport(task.id);
        }
      } finally {
        isReporting.value = false;
      }
    };

    // startPollingUnreported 启动后台任务轮询(08 §4.5 前端轮询主路径)。
    // 约 8s 一次,发现完成的任务自动在对话框展示主 Agent 汇报。
    const startPollingUnreported = (): void => {
      if (pollingTimer !== null) return; // 已启动
      pollingTimer = setInterval(() => {
        pollOnce().catch((err) => console.error('Poll unreported failed:', err));
      }, 8000);
      // 启动时立即查一次(不等首个 8s)
      pollOnce().catch((err) => console.error('Initial poll failed:', err));
    };

    const stopPollingUnreported = (): void => {
      if (pollingTimer !== null) {
        clearInterval(pollingTimer);
        pollingTimer = null;
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
      startPollingUnreported,
      stopPollingUnreported,
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
