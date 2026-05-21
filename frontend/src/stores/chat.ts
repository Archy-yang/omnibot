import { defineStore } from 'pinia';
import { ref, computed } from 'vue';
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

      isLoading.value = true;
      try {
        const response = await chatService.sendMessage(content, sessionId.value);
        addMessage(response);
      } catch (error) {
        console.error('Failed to send message:', error);
        throw error;
      } finally {
        isLoading.value = false;
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
