import { defineStore } from 'pinia';
import { ref } from 'vue';
import { memoryService } from '../services/memory';
import { useSession } from '../composables/useSession';
import type { MemoryItem } from '../types/api';

export const useMemoryStore = defineStore('memory', () => {
  const { sessionId } = useSession();

  const memories = ref<MemoryItem[]>([]);
  const isLoading = ref<boolean>(false);
  const isCreating = ref<boolean>(false);
  const isClearing = ref<boolean>(false);

  const loadMemories = async (): Promise<void> => {
    isLoading.value = true;
    try {
      const response = await memoryService.getMemories(sessionId.value);
      memories.value = response.memories;
    } finally {
      isLoading.value = false;
    }
  };

  const createMemory = async (content: string): Promise<void> => {
    isCreating.value = true;
    try {
      const response = await memoryService.createMemory({
        session_id: sessionId.value,
        content,
      });
      memories.value = [...memories.value, response.memory];
    } finally {
      isCreating.value = false;
    }
  };

  const clearMemories = async (): Promise<void> => {
    isClearing.value = true;
    try {
      await memoryService.clearMemories(sessionId.value);
      memories.value = [];
    } finally {
      isClearing.value = false;
    }
  };

  return {
    memories,
    isLoading,
    isCreating,
    isClearing,
    loadMemories,
    createMemory,
    clearMemories,
  };
});
