<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useMemory } from '@/composables/useMemory';
import { useToast } from '@/composables/useToast';

const { memories, isLoading, isCreating, isClearing, loadMemories, createMemory, clearMemories } = useMemory();
const { success, error } = useToast();

const memoryInput = ref<string>('');

const trimmedMemory = computed(() => memoryInput.value.trim());
const memoryLength = computed(() => [...trimmedMemory.value].length);
const hasMemories = computed(() => memories.value.length > 0);

const getErrorMessage = (err: unknown): string => {
  return err instanceof Error ? err.message : '服务暂时不可用，请稍后再试。';
};

const handleCreateMemory = async (): Promise<void> => {
  if (!trimmedMemory.value) {
    error('请输入要长期记住的内容。');
    return;
  }

  if (memoryLength.value > 200) {
    error('这条记忆太长了，请控制在 200 字以内。');
    return;
  }

  try {
    await createMemory(trimmedMemory.value);
    memoryInput.value = '';
    success('已记住。提醒：请不要保存密码、API Key、身份证号等敏感信息。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

const handleClearMemories = async (): Promise<void> => {
  try {
    await clearMemories();
    success('已清空你的全部长期记忆。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

onMounted(() => {
  loadMemories().catch((err: unknown) => {
    error(getErrorMessage(err));
  });
});
</script>

<template>
  <div class="space-y-4">
    <NAlert type="warning" title="安全提醒">
      请不要保存密码、API Key、身份证号等敏感信息。
    </NAlert>

    <div class="space-y-2">
      <NInput
        v-model:value="memoryInput"
        type="textarea"
        placeholder="输入希望助手长期记住的偏好、背景或项目说明"
        :autosize="{ minRows: 2, maxRows: 4 }"
        :maxlength="220"
      />
      <div class="flex items-center justify-between text-xs text-gray-500">
        <span>{{ memoryLength }}/200</span>
        <NButton
          type="primary"
          size="small"
          :loading="isCreating"
          :disabled="!trimmedMemory || memoryLength > 200"
          @click="handleCreateMemory"
        >
          保存记忆
        </NButton>
      </div>
    </div>

    <NSpin :show="isLoading">
      <NEmpty v-if="!hasMemories" description="我还没有长期记住任何信息。">
        <template #extra>
          <span class="text-sm text-gray-500">
            你可以添加一条希望助手长期记住的偏好、背景或项目说明。
          </span>
        </template>
      </NEmpty>

      <NList v-else bordered>
        <NListItem v-for="(memory, index) in memories" :key="memory.id">
          <div class="flex gap-2 text-sm leading-6">
            <span class="text-gray-400">{{ index + 1 }}.</span>
            <span class="whitespace-pre-wrap break-words">{{ memory.content }}</span>
          </div>
        </NListItem>
      </NList>
    </NSpin>

    <div class="flex justify-end">
      <NPopconfirm
        positive-text="确认清空"
        negative-text="取消"
        @positive-click="handleClearMemories"
      >
        <template #trigger>
          <NButton type="error" size="small" :loading="isClearing" :disabled="!hasMemories">
            清空全部长期记忆
          </NButton>
        </template>
        确定要清空全部长期记忆吗？清空后无法恢复。
      </NPopconfirm>
    </div>
  </div>
</template>
