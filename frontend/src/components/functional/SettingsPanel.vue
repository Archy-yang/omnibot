<script setup lang="ts">
import { ref, watch } from 'vue';
import { useToast } from '@/composables/useToast';
import type { SettingsPanelProps, SettingsPanelEmits } from '@/types/components';
import type { LLMConfig } from '@/types/api';
import { useSettingsStore } from '@/stores/settings';
import MemorySection from '@/components/functional/MemorySection.vue';

const props = defineProps<SettingsPanelProps>();
const emit = defineEmits<SettingsPanelEmits>();

const settingsStore = useSettingsStore();
const { success, error } = useToast();

// Local form state
const localConfig = ref<LLMConfig>({
  provider: 'openai',
  model: 'gpt-3.5-turbo',
  apiKey: '',
  baseUrl: '',
  temperature: 0.7,
  maxTokens: 2048,
});

// Loading states
const isSaving = ref(false);
const isClearing = ref(false);

// Sync local state when store changes
watch(
  () => settingsStore.llmConfig,
  (config) => {
    if (config) {
      localConfig.value = { ...config };
    }
  },
  { immediate: true, deep: true }
);

const themeOptions = [
  { label: '浅色', value: 'light' },
  { label: '深色', value: 'dark' },
];

const providerOptions = [
  { label: 'OpenAI', value: 'openai' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Azure OpenAI', value: 'azure' },
  { label: '通义千问', value: 'qwen' },
  { label: '豆包', value: 'doubao' },
];

const modelOptions: Record<string, { label: string; value: string }[]> = {
  openai: [
    { label: 'GPT-3.5 Turbo', value: 'gpt-3.5-turbo' },
    { label: 'GPT-4', value: 'gpt-4' },
    { label: 'GPT-4 Turbo', value: 'gpt-4-turbo' },
  ],
  anthropic: [
    { label: 'Claude 3 Opus', value: 'claude-3-opus' },
    { label: 'Claude 3 Sonnet', value: 'claude-3-sonnet' },
    { label: 'Claude 3 Haiku', value: 'claude-3-haiku' },
  ],
  azure: [
    { label: 'GPT-3.5 Turbo', value: 'gpt-3.5-turbo' },
    { label: 'GPT-4', value: 'gpt-4' },
  ],
  qwen: [
    { label: 'Qwen-Turbo', value: 'qwen-turbo' },
    { label: 'Qwen-Plus', value: 'qwen-plus' },
    { label: 'Qwen-Max', value: 'qwen-max' },
  ],
  doubao: [
    { label: 'Doubao-Pro', value: 'doubao-pro' },
    { label: 'Doubao-Lite', value: 'doubao-lite' },
  ],
};

const handleSave = async () => {
  isSaving.value = true;
  try {
    await settingsStore.updateLLMConfig(localConfig.value);
    emit('update-config', { llm: localConfig.value });
    success('配置保存成功');
    emit('close');
  } catch (err) {
    console.error('Failed to save settings:', err);
    error('配置保存失败，请重试');
  } finally {
    isSaving.value = false;
  }
};

const handleClearConfig = async () => {
  isClearing.value = true;
  try {
    await settingsStore.clearUserConfig();
    success('已恢复系统默认配置');
  } catch (err) {
    console.error('Failed to clear config:', err);
    error('清除配置失败，请重试');
  } finally {
    isClearing.value = false;
  }
};

const handleCancel = () => {
  // Reset to store values
  if (settingsStore.llmConfig) {
    localConfig.value = { ...settingsStore.llmConfig };
  }
  emit('close');
};
</script>

<template>
  <NModal
    :show="visible"
    preset="card"
    title="设置"
    style="width: 500px"
    @update:show="(v: boolean) => !v && emit('close')"
    :mask-closable="false"
  >
    <NForm label-placement="left" label-width="100px">
      <NFormItem label="主题">
        <NSelect
          v-model:value="settingsStore.theme"
          :options="themeOptions"
          @update:value="settingsStore.toggleTheme"
        />
      </NFormItem>

      <NDivider title-placement="left">LLM 配置</NDivider>

      <!-- 配置状态提示 -->
      <NAlert
        :type="settingsStore.hasUserConfig ? 'success' : 'info'"
        :title="settingsStore.configStatus"
        class="mb-4"
      />

      <div v-if="settingsStore.hasUserConfig" class="mb-4">
        <NButton
          type="error"
          size="small"
          @click="handleClearConfig"
          :loading="isClearing"
        >
          清除自定义配置，恢复使用系统默认
        </NButton>
      </div>

      <NFormItem label="服务商">
        <NSelect
          v-model:value="localConfig.provider"
          :options="providerOptions"
        />
      </NFormItem>

      <NFormItem label="模型">
        <NSelect
          v-model:value="localConfig.model"
          :options="modelOptions[localConfig.provider] || []"
        />
      </NFormItem>

      <NFormItem label="API Key">
        <NInput
          v-model:value="localConfig.apiKey"
          type="password"
          placeholder="输入 API Key"
          show-password-on="click"
        />
      </NFormItem>

      <NFormItem label="Base URL">
        <NInput
          v-model:value="localConfig.baseUrl"
          placeholder="https://api.example.com/v1"
        />
      </NFormItem>

      <NFormItem label="Temperature">
        <NSlider
          v-model:value="localConfig.temperature"
          :min="0"
          :max="2"
          :step="0.1"
        />
        <span class="ml-4 text-sm text-gray-500">
          {{ localConfig.temperature }}
        </span>
      </NFormItem>

      <NFormItem label="Max Tokens">
        <NInputNumber
          v-model:value="localConfig.maxTokens"
          :min="1"
          :max="32768"
          style="width: 100%"
        />
      </NFormItem>
    </NForm>

    <NDivider title-placement="left">长期记忆</NDivider>
    <MemorySection />

    <template #footer>
      <NSpace justify="end">
        <NButton @click="handleCancel">取消</NButton>
        <NButton type="primary" @click="handleSave" :loading="isSaving">保存</NButton>
      </NSpace>
    </template>
  </NModal>
</template>
