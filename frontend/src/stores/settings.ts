import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { LLMConfig } from '../types/api';
import type { LLMProviderOption } from '../types/llmProvider';
import { configService } from '../services/config';

// v2.1: 身份由后端 JWT 中间件解析,前端不再传 session_id
export const useSettingsStore = defineStore(
  'settings',
  () => {
    // State
    const theme = ref<'light' | 'dark'>('light');
    const llmConfig = ref<LLMConfig | null>(null);
    const showSettingsPanel = ref<boolean>(false);
    const configStatus = ref<string>('使用系统默认模型');
    const hasUserConfig = ref<boolean>(false);
    const hasEmbeddingConfig = ref<boolean>(false);
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
          provider: fullConfig.provider,
          api_key: fullConfig.apiKey,
          base_url: fullConfig.baseUrl,
          model: fullConfig.model,
          temperature: fullConfig.temperature,
          max_tokens: fullConfig.maxTokens,
          // 用户级向量配置(12-记忆系统技术方案 §5.3):
          // 已配置且现在选"使用系统默认" → 显式清除;否则透传表单值(undefined 字段不发送)
          clear_embedding: hasEmbeddingConfig.value && !fullConfig.embeddingProvider,
          embedding_provider: fullConfig.embeddingProvider,
          embedding_base_url: fullConfig.embeddingBaseUrl,
          embedding_api_key: fullConfig.embeddingApiKey,
          embedding_model: fullConfig.embeddingModel,
          embedding_dims: fullConfig.embeddingDims,
        });

        llmConfig.value = fullConfig;
        hasUserConfig.value = true;
        hasEmbeddingConfig.value = Boolean(fullConfig.embeddingProvider);
        configStatus.value = '使用你的自定义模型';
      } catch (error) {
        console.error('Failed to update LLM config:', error);
        throw error;
      }
    };

    const loadConfig = async (): Promise<void> => {
      try {
        const userConfig = await configService.getUserLLMConfig();
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
            // 向量配置回显:Key 脱敏不回填输入框,用户重新输入才发送
            embeddingProvider: userConfig.has_embedding_config ? userConfig.embedding_provider : undefined,
            embeddingBaseUrl: userConfig.has_embedding_config ? userConfig.embedding_base_url : undefined,
            embeddingModel: userConfig.has_embedding_config ? userConfig.embedding_model : undefined,
            embeddingDims: userConfig.has_embedding_config ? userConfig.embedding_dims : undefined,
            embeddingApiKey: '',
          };
          hasEmbeddingConfig.value = Boolean(userConfig.has_embedding_config);
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
        hasEmbeddingConfig.value = false;
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
        await configService.deleteUserLLMConfig();
        hasUserConfig.value = false;
        hasEmbeddingConfig.value = false;
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
      hasEmbeddingConfig,
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
