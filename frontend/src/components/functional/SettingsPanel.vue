<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import { useToast } from '@/composables/useToast';
import type { SettingsPanelProps, SettingsPanelEmits } from '@/types/components';
import type { LLMConfig } from '@/types/api';
import type { LLMProviderOption } from '@/types/llmProvider';
import { useSettingsStore } from '@/stores/settings';
import MemorySection from '@/components/functional/MemorySection.vue';

const props = defineProps<SettingsPanelProps>();
const emit = defineEmits<SettingsPanelEmits>();

const settingsStore = useSettingsStore();
const { success, error } = useToast();

// Local form state
const localConfig = ref<LLMConfig>({
  provider: 'openai',
  model: 'gpt-4o-mini',
  apiKey: '',
  baseUrl: 'https://api.openai.com/v1',
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

// Load provider options and config when panel opens
watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      settingsStore.loadProviderOptions();
      settingsStore.loadConfig();
    }
  },
  { immediate: false }
);

const themeOptions = [
  { label: '浅色', value: 'light' },
  { label: '深色', value: 'dark' },
];

// Computed provider groups

const availableProviders = computed<{ label: string; value: string; disabled?: boolean }[]>(() => {
  return settingsStore.providerOptions
    .filter((p) => p.status === 'available')
    .map((p) => ({ label: p.label, value: p.value }));
});

const disabledProviders = computed<{ label: string; value: string; disabled?: boolean }[]>(() => {
  return settingsStore.providerOptions
    .filter((p) => p.status === 'disabled')
    .map((p) => ({ label: p.label, value: p.value, disabled: true }));
});

const selectProviderOptions = computed(() => {
  const groups: { label: string; children: { label: string; value: string; disabled?: boolean }[] }[] = [];
  const avail = availableProviders.value;
  if (avail.length > 0) {
    groups.push({ label: 'OpenAI 兼容（可用）', children: avail });
  }
  const disabled = disabledProviders.value;
  if (disabled.length > 0) {
    groups.push({ label: '专用接口（暂不可用）', children: disabled });
  }
  return groups;
});

const selectedProvider = computed<LLMProviderOption | undefined>(() => {
  return settingsStore.providerOptions.find((p) => p.value === localConfig.value.provider);
});

const isNativeProviderSelected = computed<boolean>(() => {
  return selectedProvider.value?.mode === 'native';
});

const providerHelpText = computed<string>(() => {
  if (!selectedProvider.value) return '';
  return selectedProvider.value.description;
});

const handleProviderChange = (providerValue: string) => {
  const option = settingsStore.providerOptions.find((p) => p.value === providerValue);
  if (!option) return;

  if (option.status === 'disabled') {
    error(option.disabled_reason || '该服务商暂不可用');
    return;
  }

  localConfig.value.provider = option.value;
  localConfig.value.baseUrl = option.default_base_url;
  localConfig.value.model = option.default_model;
};

const handleSave = async () => {
  if (isNativeProviderSelected.value) {
    const reason = selectedProvider.value?.disabled_reason || '该专用接口暂不可用';
    error(reason);
    return;
  }

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
          :options="selectProviderOptions"
          @update:value="handleProviderChange"
        />
      </NFormItem>

      <NFormItem v-if="providerHelpText" label=" ">
        <span class="text-sm text-gray-500">{{ providerHelpText }}</span>
      </NFormItem>

      <NFormItem label="模型">
        <NInput
          v-model:value="localConfig.model"
          placeholder="输入模型名称，如 gpt-4o-mini"
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
          placeholder="https://api.openai.com/v1"
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
        <NButton
          type="primary"
          @click="handleSave"
          :loading="isSaving"
          :disabled="isNativeProviderSelected"
        >
          保存
        </NButton>
      </NSpace>
    </template>
  </NModal>
</template>
