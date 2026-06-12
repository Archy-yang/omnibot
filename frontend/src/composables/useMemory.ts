import { computed } from 'vue';
import { useMemoryStore } from '../stores/memory';

export function useMemory() {
  const memoryStore = useMemoryStore();

  const memories = computed(() => memoryStore.memories);
  const isLoading = computed(() => memoryStore.isLoading);
  const isCreating = computed(() => memoryStore.isCreating);
  const isClearing = computed(() => memoryStore.isClearing);

  return {
    memories,
    isLoading,
    isCreating,
    isClearing,
    loadMemories: memoryStore.loadMemories,
    createMemory: memoryStore.createMemory,
    clearMemories: memoryStore.clearMemories,
    deleteMemory: memoryStore.deleteMemory,
    updateMemory: memoryStore.updateMemory,
  };
}
