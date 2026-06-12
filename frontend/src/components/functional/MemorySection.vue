<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useMemory } from '@/composables/useMemory';
import { useToast } from '@/composables/useToast';

const { memories, isLoading, isCreating, isClearing, loadMemories, createMemory, clearMemories, deleteMemory, updateMemory } = useMemory();
const { success, error } = useToast();

const memoryInput = ref<string>('');
const editingId = ref<number | null>(null);
const editingContent = ref<string>('');

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

const handleDeleteMemory = async (id: number): Promise<void> => {
  try {
    await deleteMemory(id);
    success('已删除记忆。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

const startEditing = (id: number, content: string): void => {
  editingId.value = id;
  editingContent.value = content;
};

const cancelEditing = (): void => {
  editingId.value = null;
  editingContent.value = '';
};

const handleUpdateMemory = async (id: number): Promise<void> => {
  const trimmed = editingContent.value.trim();
  if (!trimmed) {
    error('请输入要长期记住的内容。');
    return;
  }

  if ([...trimmed].length > 200) {
    error('这条记忆太长了，请控制在 200 字以内。');
    return;
  }

  try {
    await updateMemory(id, trimmed);
    editingId.value = null;
    editingContent.value = '';
    success('已更新记忆。');
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
          <template v-if="editingId === memory.id">
            <div class="flex flex-col gap-2 w-full">
              <NInput
                v-model:value="editingContent"
                type="textarea"
                :autosize="{ minRows: 1, maxRows: 4 }"
                :maxlength="220"
              />
              <div class="flex items-center justify-end gap-2">
                <NButton size="tiny" @click="cancelEditing">取消</NButton>
                <NButton type="primary" size="tiny" @click="handleUpdateMemory(memory.id)">保存</NButton>
              </div>
            </div>
          </template>
          <template v-else>
            <div class="flex items-start gap-2 w-full">
              <span class="text-gray-400 text-sm leading-6">{{ index + 1 }}.</span>
              <span class="whitespace-pre-wrap break-words text-sm leading-6 flex-1">{{ memory.content }}</span>
              <div class="flex items-center gap-1 shrink-0">
                <NButton text size="tiny" @click="startEditing(memory.id, memory.content)">
                  编辑
                </NButton>
                <NPopconfirm
                  positive-text="确认删除"
                  negative-text="取消"
                  @positive-click="handleDeleteMemory(memory.id)"
                >
                  <template #trigger>
                    <NButton text type="error" size="tiny">删除</NButton>
                  </template>
                  确定要删除这条记忆吗？
                </NPopconfirm>
              </div>
            </div>
          </template>
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
