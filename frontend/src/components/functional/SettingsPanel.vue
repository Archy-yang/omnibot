<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import { useToast } from '@/composables/useToast';
import type { SettingsPanelProps, SettingsPanelEmits } from '@/types/components';
import type { LLMConfig } from '@/types/api';
import type { LLMProviderOption } from '@/types/llmProvider';
import { useSettingsStore } from '@/stores/settings';
import { APP_NAME, APP_VERSION, APP_TAGLINE, CHANNELS, ABOUT_LINKS } from '@/constants/about';

const props = defineProps<SettingsPanelProps>();
const emit = defineEmits<SettingsPanelEmits>();

const settingsStore = useSettingsStore();
const { success, error } = useToast();

// v2.0:记忆已独立为 /memory 页面,SettingsPanel 仅保留模型 + 关于两个 Tab。
const activeTab = ref<'llm' | 'about'>('llm');

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
      // 每次打开重置到模型 Tab——避免上次停留在「关于」给用户造成困惑
      activeTab.value = 'llm';
    }
  },
  { immediate: false }
);

const themeOptions = [
  { label: '浅色', value: 'light' },
  { label: '深色', value: 'dark' },
];

// Computed provider groups

const selectProviderOptions = computed(() => {
  const groups: any[] = [];
  // 兼容模式分组：mode = openai_compatible
  const compatibleProviders = settingsStore.providerOptions
    .filter((p) => p.mode === 'openai_compatible')
    .map((p) => ({
      label: p.label,
      value: p.value,
      disabled: p.status === 'disabled'
    }));
  if (compatibleProviders.length > 0) {
    groups.push({
      type: 'group',
      label: 'OpenAI 兼容模式',
      children: compatibleProviders
    });
  }
  // 专用接口分组：mode = native
  const nativeProviders = settingsStore.providerOptions
    .filter((p) => p.mode === 'native')
    .map((p) => ({
      label: p.label,
      value: p.value,
      disabled: p.status === 'disabled'
    }));
  if (nativeProviders.length > 0) {
    groups.push({
      type: 'group',
      label: '专用接口',
      children: nativeProviders
    });
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

const handleClose = () => {
  emit('close');
};
</script>

<template>
  <NModal
    :show="visible"
    preset="card"
    title="设置"
    style="width: 640px"
    @update:show="(v: boolean) => !v && emit('close')"
    :mask-closable="false"
  >
    <NTabs v-model:value="activeTab" type="line" animated>
      <!-- ============ Tab: 模型 ============ -->
      <NTabPane name="llm" tab="模型">
        <NForm label-placement="left" label-width="100px" :show-feedback="false" label-align="left">
          <!-- 配置状态提示 -->
          <NAlert
            :type="settingsStore.hasUserConfig ? 'success' : 'info'"
            :title="settingsStore.configStatus"
            size="small"
            class="mb-8"
          />

          <div v-if="settingsStore.hasUserConfig" class="mb-6">
            <NButton
              type="error"
              size="small"
              @click="handleClearConfig"
              :loading="isClearing"
            >
              清除自定义配置，恢复使用系统默认
            </NButton>
          </div>

          <NFormItem label="服务商" style="margin-bottom: 4px !important;">
            <NSelect
              v-model:value="localConfig.provider"
              :options="selectProviderOptions"
              @update:value="handleProviderChange"
            />
          </NFormItem>

          <NFormItem v-if="providerHelpText" label=" " style="margin-bottom: 4px !important;">
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

          <NFormItem label="Temperature" class="flex items-center">
            <NSlider
              v-model:value="localConfig.temperature"
              :min="0"
              :max="2"
              :step="0.1"
              class="mr-4 flex-1"
            />
            <span class="text-sm text-gray-500 w-10 text-right whitespace-nowrap">
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
      </NTabPane>

      <!-- ============ Tab: 关于 ============ -->
      <NTabPane name="about" tab="关于">
        <div class="space-y-6">
          <!-- 应用信息 -->
          <div>
            <div class="text-xl font-semibold">{{ APP_NAME }} {{ APP_VERSION }}</div>
            <div class="text-sm text-gray-500 mt-1">{{ APP_TAGLINE }}</div>
          </div>

          <!-- 主题(全局偏好) -->
          <div>
            <div class="text-sm font-medium mb-2">主题</div>
            <NSelect
              :value="settingsStore.theme"
              :options="themeOptions"
              @update:value="settingsStore.toggleTheme"
              style="width: 200px"
            />
          </div>

          <!-- 接入入口 -->
          <div>
            <div class="text-sm font-medium mb-2">已接入入口</div>
            <NList bordered>
              <NListItem v-for="channel in CHANNELS" :key="channel.type">
                <div class="flex items-start justify-between gap-4">
                  <div class="flex-1 min-w-0">
                    <div class="font-medium">{{ channel.label }}</div>
                    <div class="text-xs text-gray-500 mt-0.5">{{ channel.description }}</div>
                  </div>
                  <NTag :bordered="false" type="success" size="small">{{ channel.status }}</NTag>
                </div>
              </NListItem>
            </NList>
          </div>

          <!-- 链接 -->
          <div class="text-sm">
            <span class="text-gray-500">源码仓库:</span>
            <a
              :href="ABOUT_LINKS.repo"
              target="_blank"
              rel="noopener noreferrer"
              class="ml-2 text-blue-500 hover:underline"
            >
              {{ ABOUT_LINKS.repo }}
            </a>
          </div>
        </div>
      </NTabPane>
    </NTabs>

    <template #footer>
      <NSpace justify="end">
        <!-- 模型 Tab:保存 / 取消 -->
        <template v-if="activeTab === 'llm'">
          <NButton @click="handleCancel">取消</NButton>
          <NButton
            type="primary"
            @click="handleSave"
            :loading="isSaving"
            :disabled="isNativeProviderSelected"
          >
            保存
          </NButton>
        </template>
        <!-- 其他 Tab:仅关闭(记忆是即时保存,关于是只读) -->
        <template v-else>
          <NButton @click="handleClose">关闭</NButton>
        </template>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
:deep(.n-form-item) {
  margin-bottom: 8px !important;
}
:deep(.n-form-item--no-label) {
  margin-bottom: 8px !important;
}
:deep(.n-tab-pane) {
  padding-top: 12px;
}
</style>
