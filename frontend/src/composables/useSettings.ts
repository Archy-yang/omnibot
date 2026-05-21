import { computed } from 'vue';
import { useSettingsStore } from '../stores/settings';
import type { LLMConfig } from '../types/api';

/**
 * 设置管理 Composable
 * 封装 settingsStore 的操作
 */
export function useSettings() {
  const settingsStore = useSettingsStore();

  // State
  const theme = computed<'light' | 'dark'>(() => settingsStore.theme);
  const showSettingsPanel = computed<boolean>(() => settingsStore.showSettingsPanel);

  /**
   * 切换主题
   */
  const toggleTheme = (): void => {
    settingsStore.toggleTheme();
  };

  /**
   * 切换设置面板显示状态
   */
  const toggleSettingsPanel = (): void => {
    settingsStore.toggleSettingsPanel();
  };

  /**
   * 更新 LLM 配置
   * @param config 配置项
   */
  const updateLLMConfig = async (config: Partial<LLMConfig>): Promise<void> => {
    try {
      await settingsStore.updateLLMConfig(config);
    } catch (error) {
      console.error('Update LLM config failed in useSettings:', error);
      throw error;
    }
  };

  /**
   * 加载配置
   */
  const loadConfig = async (): Promise<void> => {
    try {
      await settingsStore.loadConfig();
    } catch (error) {
      console.error('Load config failed in useSettings:', error);
      throw error;
    }
  };

  return {
    // State
    theme,
    showSettingsPanel,
    // Methods
    toggleTheme,
    toggleSettingsPanel,
    updateLLMConfig,
    loadConfig,
  };
}
