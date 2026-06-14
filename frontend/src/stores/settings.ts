import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { LLMConfig } from '../types/api';
import type { LLMProviderOption } from '../types/llmProvider';
import { configService } from '../services/config';
import { useSession } from '../composables/useSession';

export const useSettingsStore = defineStore(
  'settings',
  () => {
    const { sessionId } = useSession();

    // State
    const theme = ref<'light' | 'dark'>('light');
    const llmConfig = ref<LLMConfig | null>(null);
    const showSettingsPanel = ref<boolean>(false);
    const configStatus = ref<string>('使用系统默认模型');
    const hasUserConfig = ref<boolean>(false);
    const providerOptions = ref<LLMProviderOption[]>([]);

    // Actions
    const toggleTheme = (): void => {
      theme.value = theme.value === 'light' ? 'dark' : 'light';
    };

    const toggleSettingsPanel = (): void => {
      showSettingsPanel.value = !showSettingsPanel.value;
    };

    const updateLLMConfig = async (config: Partial<LLMConfig>): Promise<void> => {
      try {
        const fullConfig = llmConfig.value ? { ...llmConfig.value, ...config } : config as LLMConfig;

        await configService.updateUserLLMConfig({
          session_id: sessionId.value,
          provider: fullConfig.provider,
          api_key: fullConfig.apiKey,
          base_url: fullConfig.baseUrl,
          model: fullConfig.model,
          temperature: fullConfig.temperature,
          max_tokens: fullConfig.maxTokens,
        });

        llmConfig.value = fullConfig;
        hasUserConfig.value = true;
        configStatus.value = '使用你的自定义模型';
      } catch (error) {
        console.error('Failed to update LLM config:', error);
        throw error;
      }
    };

    const loadConfig = async (): Promise<void> => {
      try {
        const userConfig = await configService.getUserLLMConfig(sessionId.value);
        hasUserConfig.value = userConfig.has_config;
        configStatus.value = userConfig.status_text;

        if (userConfig.has_config) {
          llmConfig.value = {
            provider: userConfig.provider,
            model: userConfig.model,
            baseUrl: userConfig.base_url,
            temperature: userConfig.temperature,
            maxTokens: userConfig.max_tokens,
            // API Key 不返回明文，使用时让用户重新输入或保持原样
            apiKey: '',
          };
        }
      } catch (error) {
        console.error('Failed to load config:', error);
        // 加载失败时使用默认配置
        llmConfig.value = {
          provider: 'openai',
          model: 'gpt-4o-mini',
          baseUrl: 'https://api.openai.com/v1',
          temperature: 0.7,
          maxTokens: 2048,
        };
        throw error;
      }
    };

    const loadProviderOptions = async (): Promise<void> => {
      try {
        const data = await configService.getUserLLMProviders();
        providerOptions.value = data.providers;
      } catch (error) {
        console.error('Failed to load provider options:', error);
        // 加载失败时使用 fallback 默认值
        providerOptions.value = [
          {
            value: 'openai',
            label: 'OpenAI',
            mode: 'openai_compatible',
            status: 'available',
            default_base_url: 'https://api.openai.com/v1',
            default_model: 'gpt-4o-mini',
            description: 'OpenAI 兼容服务',
            disabled_reason: '',
          },
        ];
      }
    };

    const clearUserConfig = async (): Promise<void> => {
      try {
        await configService.deleteUserLLMConfig(sessionId.value);
        hasUserConfig.value = false;
        configStatus.value = '使用系统默认模型';
        // 清除本地配置但保留默认值
        llmConfig.value = {
          provider: 'openai',
          model: 'gpt-4o-mini',
          baseUrl: 'https://api.openai.com/v1',
          temperature: 0.7,
          maxTokens: 2048,
        };
      } catch (error) {
        console.error('Failed to clear user config:', error);
        throw error;
      }
    };

    const setTheme = (newTheme: 'light' | 'dark'): void => {
      theme.value = newTheme;
    };

    return {
      // State
      theme,
      llmConfig,
      showSettingsPanel,
      configStatus,
      hasUserConfig,
      providerOptions,
      // Methods
      toggleTheme,
      toggleSettingsPanel,
      updateLLMConfig,
      loadConfig,
      loadProviderOptions,
      clearUserConfig,
      setTheme,
    };
  },
  {
    persist: {
      key: 'settings-store',
      storage: localStorage,
      pick: ['theme'], // 不持久化 llmConfig，始终从后端加载
    },
  }
);
