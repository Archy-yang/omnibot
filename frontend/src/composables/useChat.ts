import { computed } from 'vue';
import { useChatStore } from '../stores/chat';
import type { Message } from '../types/chat';

/**
 * 聊天核心逻辑 Composable
 * 封装 chatStore 的常用操作，提供便捷的计算属性和方法
 */
export function useChat() {
  const chatStore = useChatStore();

  // State - 直接从 store 获取响应式数据
  const messages = computed<Message[]>(() => chatStore.messages);
  const isLoading = computed<boolean>(() => chatStore.isLoading);
  const lastMessage = computed<Message | null>(() => chatStore.lastMessage);

  /**
   * 发送消息
   * @param content 消息内容
   * @param isAgentMode 是否使用Agent模式
   */
  const sendMessage = async (content: string, isAgentMode: boolean = false): Promise<void> => {
    try {
      await chatStore.sendMessage(content, isAgentMode);
    } catch (error) {
      console.error('Send message failed in useChat:', error);
      throw error;
    }
  };

  /**
   * 加载聊天历史记录
   */
  const loadHistory = async (): Promise<void> => {
    try {
      await chatStore.loadHistory();
    } catch (error) {
      console.error('Load history failed in useChat:', error);
      throw error;
    }
  };

  /**
   * 清空所有消息
   */
  const clearMessages = (): void => {
    chatStore.clearMessages();
  };

  return {
    // State
    messages,
    isLoading,
    lastMessage,
    // Methods
    sendMessage,
    loadHistory,
    clearMessages,
  };
}
