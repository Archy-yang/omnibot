import { defineStore } from 'pinia';
import { ref } from 'vue';
import { memoryService } from '../services/memory';
import type { MemoryItem } from '../types/api';

// v2.1: 身份由后端 JWT 中间件解析,前端不再传 session_id
export const useMemoryStore = defineStore('memory', () => {
  const memories = ref<MemoryItem[]>([]);
  const isLoading = ref<boolean>(false);
  const isCreating = ref<boolean>(false);
  const isClearing = ref<boolean>(false);

  const loadMemories = async (): Promise<void> => {
    isLoading.value = true;
    try {
      const response = await memoryService.getMemories();
      memories.value = response.memories;
    } finally {
      isLoading.value = false;
    }
  };

  const createMemory = async (content: string): Promise<void> => {
    isCreating.value = true;
    try {
      const response = await memoryService.createMemory({ content });
      memories.value = [...memories.value, response.memory];
    } finally {
      isCreating.value = false;
    }
  };

  const clearMemories = async (): Promise<void> => {
    isClearing.value = true;
    try {
      await memoryService.clearMemories();
      memories.value = [];
    } finally {
      isClearing.value = false;
    }
  };

  const deleteMemory = async (id: number): Promise<void> => {
    await memoryService.deleteMemory(id);
    memories.value = memories.value.filter((m) => m.id !== id);
  };

  const updateMemory = async (id: number, content: string): Promise<void> => {
    const response = await memoryService.updateMemory(id, { content });
    const index = memories.value.findIndex((m) => m.id === id);
    if (index !== -1) {
      memories.value[index] = response.memory;
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
    deleteMemory,
    updateMemory,
  };
});
