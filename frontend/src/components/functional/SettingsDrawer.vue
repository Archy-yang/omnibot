<script setup lang="ts">
/**
 * SettingsDrawer — v2.0 设置抽屉
 *
 * 设计稿:docs/60-设计/omnibot-prototype/pages/v2-settings.html。
 * 替代 v1.10 NaiveUI Modal 形态的 SettingsPanel.vue。
 *
 * 关键变化:
 *   - 形态:NaiveUI Modal 640px → 右侧 400px 抽屉
 *   - 结构:Tabs(模型/关于)→ 竖排两个 section,无 Tab
 *   - 控件:NSelect/NInput/NSlider/NInputNumber 全部换为原生
 *     <select>/<input>/<input type="range">,匹配设计稿的轻量风格
 *
 * 业务逻辑与原 SettingsPanel 等价(provider 预设/校验/保存/清除/主题)。
 */
import { ref, watch, computed, onUnmounted } from 'vue';
import { useRouter } from 'vue-router';
import { useToast } from '@/composables/useToast';
import type { LLMConfig, SkillItem } from '@/types/api';
import { skillService } from '@/services/skill';
import type { LLMProviderOption } from '@/types/llmProvider';
import { useSettingsStore } from '@/stores/settings';
import { useAuthStore } from '@/stores/user';
import { APP_NAME, APP_VERSION, APP_TAGLINE, CHANNELS, ABOUT_LINKS } from '@/constants/about';
import { channelBindingService } from '@/services/channelBinding';
import DrawerShell from '@/components/layout/DrawerShell.vue';

const props = defineProps<{
  visible: boolean;
}>();

const emit = defineEmits<{
  close: [];
  'update-config': [config: { llm: LLMConfig }];
}>();

const settingsStore = useSettingsStore();
const authStore = useAuthStore();
const router = useRouter();
const { success, error } = useToast();

// Local form state — 编辑时不直接改 store,取消时恢复
const localConfig = ref<LLMConfig>({
  provider: 'openai',
  model: 'gpt-4o-mini',
  apiKey: '',
  baseUrl: 'https://api.openai.com/v1',
  temperature: 0.7,
  maxTokens: 2048,
});

const isSaving = ref(false);
const isClearing = ref(false);
const showApiKey = ref(false);

// ===== v2.2 飞书绑定 =====
const feishuBound = ref<boolean>(false);
const wechatBound = ref<boolean>(false);
const feishuCode = ref<string>('');
const feishuCodeExpiresIn = ref<number>(0);
const feishuCodeLoading = ref<boolean>(false);
let feishuTimer: ReturnType<typeof setInterval> | null = null;

const clearFeishuTimer = () => {
  if (feishuTimer) {
    clearInterval(feishuTimer);
    feishuTimer = null;
  }
};

const startFeishuCountdown = (seconds: number) => {
  clearFeishuTimer();
  feishuCodeExpiresIn.value = seconds;
  feishuTimer = setInterval(() => {
    feishuCodeExpiresIn.value -= 1;
    if (feishuCodeExpiresIn.value <= 0) {
      clearFeishuTimer();
      feishuCode.value = ''; // 过期清空
    }
  }, 1000);
};

const loadChannelBinding = async () => {
  try {
    const status = await channelBindingService.getBindingStatus();
    feishuBound.value = status.feishu_bound;
    wechatBound.value = status.wechat_bound;
  } catch (err) {
    // 静默失败,不阻断抽屉
    console.error('Failed to load channel binding:', err);
  }
};

const handleGenerateBindCode = async () => {
  feishuCodeLoading.value = true;
  try {
    const data = await channelBindingService.generateBindCode();
    feishuCode.value = data.code;
    startFeishuCountdown(data.expires_in || 300);
    success('绑定码已生成,5 分钟内有效');
  } catch (err) {
    error(err instanceof Error ? err.message : '生成绑定码失败');
  } finally {
    feishuCodeLoading.value = false;
  }
};

const feishuCountdownText = computed(() => {
  const s = feishuCodeExpiresIn.value;
  if (s <= 0) return '已过期';
  const m = Math.floor(s / 60);
  const sec = s % 60;
  return `${m}:${sec.toString().padStart(2, '0')}`;
});

onUnmounted(() => clearFeishuTimer());

// Sync local from store
watch(
  () => settingsStore.llmConfig,
  (config) => {
    if (config) localConfig.value = { ...config };
  },
  { immediate: true, deep: true }
);

// 抽屉打开时拉 provider 列表 + 当前配置 + 飞书绑定状态
watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      settingsStore.loadProviderOptions();
      settingsStore.loadConfig();
      showApiKey.value = false; // 每次打开重置密码显示态(安全)
      loadChannelBinding();
      loadSkills();
    }
  }
);

// ===== Provider 预设 =====
const compatibleProviders = computed(() =>
  settingsStore.providerOptions.filter((p) => p.mode === 'openai_compatible')
);
const nativeProviders = computed(() =>
  settingsStore.providerOptions.filter((p) => p.mode === 'native')
);

const selectedProvider = computed<LLMProviderOption | undefined>(() =>
  settingsStore.providerOptions.find((p) => p.value === localConfig.value.provider)
);

const isNativeProviderSelected = computed<boolean>(
  () => selectedProvider.value?.mode === 'native'
);

const providerHelpText = computed<string>(() => selectedProvider.value?.description ?? '');

const handleProviderChange = (e: Event) => {
  const value = (e.target as HTMLSelectElement).value;
  const option = settingsStore.providerOptions.find((p) => p.value === value);
  if (!option) return;
  if (option.status === 'disabled') {
    error(option.disabled_reason || '该服务商暂不可用');
    return;
  }
  localConfig.value.provider = option.value;
  localConfig.value.baseUrl = option.default_base_url;
  localConfig.value.model = option.default_model;
};

// ===== 保存 / 取消 / 清除 =====
const handleSave = async () => {
  if (isNativeProviderSelected.value) {
    const reason = selectedProvider.value?.disabled_reason || '该专用接口暂不可用';
    error(reason);
    return;
  }
  isSaving.value = true;
  try {
    const embeddingError = validateEmbeddingConfig();
    if (embeddingError) {
      error(embeddingError);
      return;
    }
    await settingsStore.updateLLMConfig(localConfig.value);
    emit('update-config', { llm: localConfig.value });
    success('配置保存成功');
    emit('close');
  } catch (err) {
    console.error('Failed to save settings:', err);
    error('配置保存失败,请重试');
  } finally {
    isSaving.value = false;
  }
};

const handleCancel = () => {
  if (settingsStore.llmConfig) {
    localConfig.value = { ...settingsStore.llmConfig };
  }
  emit('close');
};

const handleClearConfig = async () => {
  if (!window.confirm('确定清除自定义配置吗?之后将使用系统默认模型。')) return;
  isClearing.value = true;
  try {
    await settingsStore.clearUserConfig();
    success('已恢复系统默认配置');
  } catch (err) {
    console.error('Failed to clear config:', err);
    error('清除配置失败,请重试');
  } finally {
    isClearing.value = false;
  }
};

// 主题切换(保留 v1.10 功能,设计稿未明示但不删)
const handleThemeChange = (e: Event) => {
  const next = (e.target as HTMLSelectElement).value as 'light' | 'dark';
  if (next !== settingsStore.theme) settingsStore.toggleTheme();
};

// v2.1 登出:清 token → 跳登录页
const handleLogout = () => {
  authStore.clearToken();
  emit('close');
  router.push('/login');
};

// Temperature 显示值:LLMConfig.temperature 是可选,默认 0.7
const temperatureDisplay = computed(() => {
  const t = localConfig.value.temperature;
  return typeof t === 'number' ? t.toFixed(1) : '0.7';
});

// 数字输入框:确保 maxTokens 是 number
const handleMaxTokensInput = (e: Event) => {
  const v = parseInt((e.target as HTMLInputElement).value, 10);
  if (!Number.isNaN(v)) localConfig.value.maxTokens = v;
};

// ===== 用户级向量配置(12-记忆系统技术方案 §5.3) =====
// 语义:provider 选"使用系统默认"= 不覆盖;已配置过则保存时显式清除(后端 clear_embedding)。
const handleEmbeddingProviderChange = (e: Event) => {
  const value = (e.target as HTMLSelectElement).value;
  if (!value) {
    // 切回系统默认:清空表单值(store 保存时发 clear_embedding)
    localConfig.value.embeddingProvider = undefined;
    localConfig.value.embeddingBaseUrl = undefined;
    localConfig.value.embeddingModel = undefined;
    localConfig.value.embeddingDims = undefined;
    localConfig.value.embeddingApiKey = undefined;
    return;
  }
  localConfig.value.embeddingProvider = value;
};

const handleEmbeddingDimsInput = (e: Event) => {
  const v = parseInt((e.target as HTMLInputElement).value, 10);
  localConfig.value.embeddingDims = Number.isNaN(v) ? undefined : v;
};

// 保存前校验:镜像后端——向量配置五要素要么全空,要么齐全
const validateEmbeddingConfig = (): string => {
  const cfg = localConfig.value;
  if (!cfg.embeddingProvider) return '';
  if (!cfg.embeddingBaseUrl || !cfg.embeddingModel || !cfg.embeddingDims || !cfg.embeddingApiKey) {
    return '向量配置需填写完整(含 API Key 与维度),或选择"使用系统默认"';
  }
  if (cfg.embeddingDims <= 0) return '向量维度必须为正整数';
  return '';
};

const showEmbeddingApiKey = ref(false);

// ===== 技能管理(13-插件系统):清单 + 启停 =====
const skills = ref<SkillItem[]>([]);
const skillsLoading = ref(false);
// 记录切换中的技能名,行内展示 loading 且防重复点击
const togglingSkills = ref<Set<string>>(new Set());

const loadSkills = async () => {
  skillsLoading.value = true;
  try {
    const data = await skillService.listSkills();
    skills.value = data.skills;
  } catch (err) {
    console.error('Failed to load skills:', err);
    // 静默失败:技能区拉不到不阻断抽屉其他功能
  } finally {
    skillsLoading.value = false;
  }
};

const handleSkillToggle = async (skill: SkillItem, event: Event) => {
  const enabled = (event.target as HTMLInputElement).checked;
  if (togglingSkills.value.has(skill.name)) {
    (event.target as HTMLInputElement).checked = !enabled; // 回滚 UI
    return;
  }
  togglingSkills.value.add(skill.name);
  try {
    await skillService.updateSkill(skill.name, enabled);
    skill.enabled = enabled;
    success(enabled ? `已启用「${skill.display_name || skill.name}」` : `已停用「${skill.display_name || skill.name}」`);
  } catch (err) {
    (event.target as HTMLInputElement).checked = !enabled; // 失败回滚
    error(err instanceof Error ? err.message : '更新技能状态失败');
  } finally {
    togglingSkills.value.delete(skill.name);
  }
};

const skillSourceLabel = (source: string): string =>
  source === 'mcp' ? '外部接入' : '内置';
</script>

<template>
  <DrawerShell :visible="visible" title="设置" @close="emit('close')">
    <!-- ===== 模型配置 section ===== -->
    <div class="section-title">模型配置</div>

    <!-- 配置状态提示条 -->
    <div class="config-hint">
      <span
        class="config-hint-dot"
        :class="{ 'is-custom': settingsStore.hasUserConfig }"
      ></span>
      <span class="config-hint-text">{{ settingsStore.configStatus }}</span>
    </div>

    <!-- 清除自定义配置(仅当已自定义) -->
    <div v-if="settingsStore.hasUserConfig" class="clear-config-row">
      <button
        type="button"
        class="clear-config-btn"
        :disabled="isClearing"
        @click="handleClearConfig"
      >
        {{ isClearing ? '清除中...' : '清除自定义配置,恢复系统默认' }}
      </button>
    </div>

    <!-- 服务商 -->
    <div class="form-field">
      <label class="form-label" for="settings-provider">服务商</label>
      <select
        id="settings-provider"
        class="form-select"
        :value="localConfig.provider"
        @change="handleProviderChange"
      >
        <optgroup v-if="compatibleProviders.length > 0" label="OpenAI 兼容模式">
          <option
            v-for="p in compatibleProviders"
            :key="p.value"
            :value="p.value"
            :disabled="p.status === 'disabled'"
          >
            {{ p.label }}
          </option>
        </optgroup>
        <optgroup v-if="nativeProviders.length > 0" label="专用接口">
          <option
            v-for="p in nativeProviders"
            :key="p.value"
            :value="p.value"
            :disabled="p.status === 'disabled'"
          >
            {{ p.label }}
          </option>
        </optgroup>
      </select>
      <div v-if="providerHelpText" class="form-hint">{{ providerHelpText }}</div>
    </div>

    <!-- 模型名称 -->
    <div class="form-field">
      <label class="form-label" for="settings-model">模型名称</label>
      <input
        id="settings-model"
        type="text"
        class="form-input"
        v-model="localConfig.model"
        placeholder="如 gpt-4o、deepseek-chat"
      />
    </div>

    <!-- API Key -->
    <div class="form-field">
      <label class="form-label" for="settings-apikey">API Key</label>
      <div class="password-wrapper">
        <input
          id="settings-apikey"
          :type="showApiKey ? 'text' : 'password'"
          class="form-input"
          v-model="localConfig.apiKey"
          placeholder="输入 API Key"
        />
        <button
          type="button"
          class="password-toggle"
          :title="showApiKey ? '隐藏' : '显示'"
          :aria-label="showApiKey ? '隐藏 API Key' : '显示 API Key'"
          @click="showApiKey = !showApiKey"
        >
          <svg v-if="showApiKey" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/>
            <line x1="1" y1="1" x2="23" y2="23"/>
          </svg>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>
            <circle cx="12" cy="12" r="3"/>
          </svg>
        </button>
      </div>
    </div>

    <!-- Base URL -->
    <div class="form-field">
      <label class="form-label" for="settings-baseurl">Base URL</label>
      <input
        id="settings-baseurl"
        type="text"
        class="form-input"
        v-model="localConfig.baseUrl"
        placeholder="留空使用默认地址"
      />
    </div>

    <!-- Temperature -->
    <div class="form-field">
      <label class="form-label">Temperature</label>
      <div class="temperature-field">
        <input
          type="range"
          class="temperature-slider"
          min="0"
          max="2"
          step="0.1"
          v-model.number="localConfig.temperature"
        />
        <span class="temperature-value">{{ temperatureDisplay }}</span>
      </div>
    </div>

    <!-- Max Tokens -->
    <div class="form-field">
      <label class="form-label" for="settings-maxtokens">Max Tokens</label>
      <input
        id="settings-maxtokens"
        type="number"
        class="form-input"
        :value="localConfig.maxTokens"
        min="1"
        max="32768"
        placeholder="4096"
        @input="handleMaxTokensInput"
      />
    </div>

    <!-- ===== 向量模型(可选,用户级覆盖系统默认) ===== -->
    <div class="embedding-block">
      <div class="entry-label">向量模型（可选）</div>
      <div class="form-field">
        <label class="form-label" for="settings-embedding-provider">Embedding 服务</label>
        <select
          id="settings-embedding-provider"
          class="form-select"
          :value="localConfig.embeddingProvider ?? ''"
          @change="handleEmbeddingProviderChange"
        >
          <option value="">使用系统默认</option>
          <option value="openai_compatible">OpenAI 兼容（千帆 / DashScope 等）</option>
          <option value="ollama">Ollama 本地</option>
        </select>
        <div class="form-hint">用于记忆的语义检索。留空使用系统默认；修改后保存需重新输入 API Key。</div>
      </div>

      <template v-if="localConfig.embeddingProvider">
        <div class="form-field">
          <label class="form-label" for="settings-embedding-baseurl">向量 Base URL</label>
          <input
            id="settings-embedding-baseurl"
            type="text"
            class="form-input"
            v-model="localConfig.embeddingBaseUrl"
            :placeholder="localConfig.embeddingProvider === 'ollama' ? 'http://localhost:11434' : 'https://qianfan.baidubce.com/v2'"
          />
        </div>
        <div class="form-field">
          <label class="form-label" for="settings-embedding-apikey">向量 API Key</label>
          <div class="password-wrapper">
            <input
              id="settings-embedding-apikey"
              :type="showEmbeddingApiKey ? 'text' : 'password'"
              class="form-input"
              v-model="localConfig.embeddingApiKey"
              :placeholder="settingsStore.hasEmbeddingConfig ? '已配置，输入新值可更换' : '输入向量 API Key'"
            />
            <button
              type="button"
              class="password-toggle"
              :title="showEmbeddingApiKey ? '隐藏' : '显示'"
              :aria-label="showEmbeddingApiKey ? '隐藏向量 API Key' : '显示向量 API Key'"
              @click="showEmbeddingApiKey = !showEmbeddingApiKey"
            >
              <svg v-if="showEmbeddingApiKey" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/>
                <line x1="1" y1="1" x2="23" y2="23"/>
              </svg>
              <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/>
                <circle cx="12" cy="12" r="3"/>
              </svg>
            </button>
          </div>
        </div>
        <div class="form-field embedding-row">
          <div class="embedding-col">
            <label class="form-label" for="settings-embedding-model">向量模型</label>
            <input
              id="settings-embedding-model"
              type="text"
              class="form-input"
              v-model="localConfig.embeddingModel"
              placeholder="如 bge-large-zh、qwen3-embedding:0.6b"
            />
          </div>
          <div class="embedding-col">
            <label class="form-label" for="settings-embedding-dims">向量维度</label>
            <input
              id="settings-embedding-dims"
              type="number"
              class="form-input"
              :value="localConfig.embeddingDims"
              min="1"
              placeholder="如 1024"
              @input="handleEmbeddingDimsInput"
            />
          </div>
        </div>
      </template>
    </div>

    <!-- 模型 section 底部按钮 -->
    <div class="form-actions">
      <button type="button" class="btn btn-cancel" @click="handleCancel">取消</button>
      <button
        type="button"
        class="btn btn-save"
        :disabled="isSaving || isNativeProviderSelected"
        @click="handleSave"
      >
        {{ isSaving ? '保存中...' : '保存' }}
      </button>
    </div>

    <!-- ===== 技能 section(13-插件系统):助手本领清单,可逐个启停 ===== -->
    <div class="section-skills">
      <div class="section-title">技能</div>

      <div v-if="skillsLoading" class="skills-loading">加载中...</div>
      <div v-else-if="skills.length === 0" class="skills-empty">
        暂无可用技能
      </div>
      <ul v-else class="skill-list">
        <li
          v-for="skill in skills"
          :key="skill.name"
          class="skill-item"
          :class="{ 'is-disabled': !skill.available }"
        >
          <div class="skill-info">
            <div class="skill-name-row">
              <span class="skill-name">{{ skill.display_name || skill.name }}</span>
              <span class="skill-source-badge" :class="`is-${skill.source}`">
                {{ skillSourceLabel(skill.source) }}
              </span>
            </div>
            <div class="skill-desc">{{ skill.description }}</div>
          </div>
          <label class="skill-switch" :title="skill.available ? '' : '该技能暂不可用'">
            <input
              type="checkbox"
              role="switch"
              :aria-label="`启用${skill.display_name || skill.name}`"
              :checked="skill.enabled"
              :disabled="!skill.available || togglingSkills.has(skill.name)"
              @change="handleSkillToggle(skill, $event)"
            />
            <span class="skill-switch-slider"></span>
          </label>
        </li>
      </ul>
    </div>

    <!-- ===== 关于 section ===== -->
    <div class="section-about">
      <div class="section-title">关于</div>

      <!-- 应用信息 -->
      <div class="app-info">
        <div class="app-name">{{ APP_NAME }}</div>
        <div class="app-version">{{ APP_VERSION }}</div>
        <div class="app-desc">{{ APP_TAGLINE }}</div>
      </div>

      <!-- 主题 -->
      <div class="theme-row">
        <label class="form-label" for="settings-theme">主题</label>
        <select
          id="settings-theme"
          class="form-select theme-select"
          :value="settingsStore.theme"
          @change="handleThemeChange"
        >
          <option value="light">浅色</option>
          <option value="dark">深色</option>
        </select>
      </div>

      <!-- v2.3 渠道绑定(飞书 + 微信,通用绑定码) -->
      <div class="feishu-bind-block">
        <div class="entry-label">渠道绑定</div>

        <!-- 各渠道绑定状态 -->
        <div class="channel-status-row">
          <div class="channel-status-item" :class="{ bound: feishuBound }">
            <span class="channel-dot"></span>
            <span class="channel-name">飞书</span>
            <span class="channel-state">{{ feishuBound ? '已绑定' : '未绑定' }}</span>
          </div>
          <div class="channel-status-item" :class="{ bound: wechatBound }">
            <span class="channel-dot"></span>
            <span class="channel-name">微信</span>
            <span class="channel-state">{{ wechatBound ? '已绑定' : '未绑定' }}</span>
          </div>
        </div>

        <!-- 全部已绑:不展示出码区 -->
        <template v-if="!(feishuBound && wechatBound)">
          <!-- 未生成码:获取按钮 -->
          <button
            v-if="!feishuCode"
            type="button"
            class="feishu-code-btn"
            :disabled="feishuCodeLoading"
            @click="handleGenerateBindCode"
          >
            {{ feishuCodeLoading ? '生成中...' : '获取绑定码' }}
          </button>

          <!-- 绑定码已生成 -->
          <div v-else class="feishu-code-display">
            <div class="feishu-code-text">{{ feishuCode }}</div>
            <div class="feishu-code-meta">
              <span class="feishu-countdown" :class="{ expired: feishuCodeExpiresIn <= 0 }">
                {{ feishuCountdownText }}
              </span>
              <button
                type="button"
                class="feishu-regen-btn"
                :disabled="feishuCodeLoading"
                @click="handleGenerateBindCode"
              >
                重新获取
              </button>
            </div>
            <p class="feishu-code-tip">
              在你要绑定的渠道(飞书或微信)向机器人发送：<code>绑定 {{ feishuCode }}</code>
            </p>
          </div>
        </template>
      </div>

      <!-- 已接入入口 -->
      <div class="entry-label">已接入入口</div>
      <div class="entry-item" v-for="channel in CHANNELS" :key="channel.type">
        <div class="entry-info">
          <div class="entry-name">{{ channel.label }}</div>
          <div class="entry-desc">{{ channel.description }}</div>
        </div>
        <span class="entry-badge">{{ channel.status }}</span>
      </div>

      <!-- 源码链接 -->
      <div class="source-link">
        源码仓库 · <a :href="ABOUT_LINKS.repo" target="_blank" rel="noopener noreferrer">{{ ABOUT_LINKS.repo }}</a>
      </div>

      <!-- v2.1 登出按钮 -->
      <button type="button" class="logout-btn" @click="handleLogout">登出</button>
    </div>

    <div class="drawer-footer-spacer"></div>
  </DrawerShell>
</template>

<style scoped>
/* ===== 区块标题 ===== */
.section-title {
  font-size: 14px;
  font-weight: 600;
  color: #171717;
  padding-bottom: 12px;
  border-bottom: 1px solid #f0f0f0;
  margin-bottom: 16px;
}

/* ===== 配置状态提示 ===== */
.config-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  background: #f8f9fa;
  border-radius: 8px;
  margin-bottom: 16px;
}
.config-hint-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #9ca3af;
  flex-shrink: 0;
}
.config-hint-dot.is-custom {
  background: #10a37f;
}
.config-hint-text {
  font-size: 13px;
  color: #888;
  line-height: 1.5;
}

.clear-config-row {
  margin-bottom: 16px;
}
.clear-config-btn {
  font-size: 12px;
  color: #ef4444;
  background: none;
  border: none;
  cursor: pointer;
  font-family: inherit;
  padding: 0;
}
.clear-config-btn:hover:not(:disabled) {
  text-decoration: underline;
}
.clear-config-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ===== 表单字段 ===== */
.form-field {
  margin-bottom: 16px;
}
.form-label {
  display: block;
  font-size: 13px;
  color: #999;
  margin-bottom: 6px;
}
.form-hint {
  font-size: 12px;
  color: #999;
  margin-top: 6px;
  line-height: 1.4;
}
.form-input,
.form-select {
  width: 100%;
  padding: 8px 12px;
  border: 1px solid #e5e5e5;
  border-radius: 8px;
  font-size: 14px;
  color: #171717;
  background: #ffffff;
  outline: none;
  transition: border-color 150ms ease, box-shadow 150ms ease;
  font-family: inherit;
}
.form-input:focus,
.form-select:focus {
  border-color: #2080f0;
  box-shadow: 0 0 0 2px rgba(32, 128, 240, 0.1);
}
.form-input::placeholder {
  color: #bbb;
}
.form-select {
  appearance: none;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%23999' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpolyline points='6 9 12 15 18 9'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 12px center;
  padding-right: 32px;
  cursor: pointer;
}

/* ===== 密码输入框 + 显隐切换 ===== */
.password-wrapper {
  position: relative;
}
.password-wrapper .form-input {
  padding-right: 40px;
}
.password-toggle {
  position: absolute;
  right: 8px;
  top: 50%;
  transform: translateY(-50%);
  width: 28px;
  height: 28px;
  border: none;
  background: transparent;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: background 150ms ease;
  color: #999;
}
.password-toggle:hover {
  background: #f5f5f5;
}

/* ===== Temperature 滑块 ===== */
.temperature-field {
  display: flex;
  align-items: center;
  gap: 12px;
}
.temperature-slider {
  flex: 1;
  -webkit-appearance: none;
  appearance: none;
  height: 4px;
  background: #e5e5e5;
  border-radius: 2px;
  outline: none;
  cursor: pointer;
}
.temperature-slider::-webkit-slider-thumb {
  -webkit-appearance: none;
  appearance: none;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #10a37f;
  border: 2px solid #ffffff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.15);
  cursor: pointer;
  transition: transform 100ms ease;
}
.temperature-slider::-webkit-slider-thumb:hover {
  transform: scale(1.15);
}
.temperature-slider::-moz-range-thumb {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #10a37f;
  border: 2px solid #ffffff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.15);
  cursor: pointer;
}
.temperature-value {
  font-size: 13px;
  color: #666;
  min-width: 28px;
  text-align: right;
  font-variant-numeric: tabular-nums;
}

/* ===== 向量模型区块 ===== */
.embedding-block {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid #f0f0f0;
}
.embedding-row {
  display: flex;
  gap: 12px;
}
.embedding-col {
  flex: 1;
  min-width: 0;
}

/* ===== 模型 section 底部按钮 ===== */
.form-actions {
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid #f0f0f0;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.btn {
  padding: 8px 16px;
  border-radius: 8px;
  font-size: 14px;
  border: none;
  cursor: pointer;
  transition: all 150ms ease;
  font-family: inherit;
}
.btn-cancel {
  background: #f5f5f5;
  color: #666;
}
.btn-cancel:hover {
  background: #ebebeb;
}
.btn-save {
  background: #171717;
  color: #ffffff;
}
.btn-save:hover:not(:disabled) {
  background: #333;
}
.btn-save:disabled {
  background: #d4d4d4;
  cursor: not-allowed;
}

/* ===== 关于 section ===== */
.section-about {
  margin-top: 24px;
}
.app-info {
  margin-bottom: 16px;
}
.app-name {
  font-size: 16px;
  font-weight: 600;
  color: #171717;
}
.app-version {
  font-size: 13px;
  color: #999;
  margin-top: 2px;
}
.app-desc {
  font-size: 13px;
  color: #999;
  margin-top: 4px;
}

/* 主题切换 — 紧凑布局 */
.theme-row {
  margin-bottom: 16px;
}
.theme-select {
  width: 120px;
}

.entry-label {
  font-size: 13px;
  font-weight: 500;
  color: #999;
  margin-bottom: 8px;
}
.entry-item {
  padding: 10px 12px;
  background: #f8f9fa;
  border-radius: 8px;
  margin-bottom: 6px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.entry-item:last-of-type {
  margin-bottom: 0;
}
.entry-info {
  flex: 1;
  min-width: 0;
}
.entry-name {
  font-size: 13px;
  color: #333;
  font-weight: 500;
}
.entry-desc {
  font-size: 12px;
  color: #999;
  margin-top: 1px;
}
.entry-badge {
  background: #e8f5e9;
  color: #10a37f;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
  flex-shrink: 0;
  margin-left: 12px;
}

.source-link {
  margin-top: 16px;
  font-size: 13px;
  color: #999;
  word-break: break-all;
}
.source-link a {
  color: #10a37f;
  text-decoration: none;
  transition: opacity 150ms ease;
}
.source-link a:hover {
  opacity: 0.8;
}

/* v2.1 登出按钮 */
.logout-btn {
  margin-top: 20px;
  padding: 10px 16px;
  width: 100%;
  background: #ffffff;
  color: #b91c1c;
  border: 1px solid #fecaca;
  border-radius: 8px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s;
}
.logout-btn:hover {
  background: #fef2f2;
}

/* v2.3 渠道绑定区块 */
.feishu-bind-block {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid #f0f0f0;
}
.channel-status-row {
  display: flex;
  gap: 12px;
  margin-bottom: 12px;
}
.channel-status-item {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 12px;
  background: #f9fafb;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  font-size: 13px;
}
.channel-status-item.bound {
  background: #f0fdf4;
  border-color: #bbf7d0;
}
.channel-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #d1d5db;
}
.channel-status-item.bound .channel-dot {
  background: #22c55e;
}
.channel-name {
  font-weight: 500;
  color: #171717;
}
.channel-state {
  margin-left: auto;
  color: #6b7280;
}
.channel-status-item.bound .channel-state {
  color: #15803d;
}

.feishu-code-btn {
  width: 100%;
  padding: 10px 16px;
  background: #10a37f;
  color: #ffffff;
  border: none;
  border-radius: 8px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.15s;
}
.feishu-code-btn:hover:not(:disabled) {
  background: #0d8f6f;
}
.feishu-code-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
.feishu-code-display {
  padding: 16px;
  background: #f9fafb;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
}
.feishu-code-text {
  font-size: 32px;
  font-weight: 700;
  letter-spacing: 6px;
  color: #10a37f;
  text-align: center;
  font-family: 'SF Mono', 'Menlo', monospace;
}
.feishu-code-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 12px;
}
.feishu-countdown {
  font-size: 12px;
  color: #6b7280;
}
.feishu-countdown.expired {
  color: #b91c1c;
}
.feishu-regen-btn {
  padding: 4px 12px;
  background: #ffffff;
  color: #10a37f;
  border: 1px solid #10a37f;
  border-radius: 6px;
  font-size: 12px;
  cursor: pointer;
}
.feishu-regen-btn:hover:not(:disabled) {
  background: #f0fdf4;
}
.feishu-regen-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
.feishu-code-tip {
  margin: 12px 0 0;
  font-size: 12px;
  color: #6b7280;
  line-height: 1.5;
}
.feishu-code-tip code {
  padding: 2px 6px;
  background: #e5e7eb;
  border-radius: 4px;
  font-family: 'SF Mono', 'Menlo', monospace;
  color: #171717;
}

.drawer-footer-spacer {
  height: 32px;
}

/* ===== 技能 section(13-插件系统) ===== */
.section-skills {
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid #f0f0f0;
}
.skills-loading,
.skills-empty {
  font-size: 13px;
  color: #9ca3af;
  padding: 8px 0;
}
.skill-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.skill-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 0;
  border-bottom: 1px solid #f5f5f5;
}
.skill-item:last-child {
  border-bottom: none;
}
.skill-item.is-disabled .skill-name,
.skill-item.is-disabled .skill-desc {
  color: #9ca3af;
}
.skill-info {
  flex: 1;
  min-width: 0;
}
.skill-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.skill-name {
  font-size: 14px;
  font-weight: 500;
  color: #171717;
}
.skill-source-badge {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  background: #f3f4f6;
  color: #6b7280;
  white-space: nowrap;
}
.skill-source-badge.is-mcp {
  background: #eff6ff;
  color: #1d4ed8;
}
.skill-desc {
  font-size: 12px;
  color: #6b7280;
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

/* 开关(checkbox + slider,无现成组件,轻量原生实现) */
.skill-switch {
  position: relative;
  flex-shrink: 0;
  width: 36px;
  height: 20px;
  cursor: pointer;
}
.skill-switch input {
  position: absolute;
  opacity: 0;
  width: 100%;
  height: 100%;
  margin: 0;
  cursor: pointer;
}
.skill-switch input:disabled {
  cursor: not-allowed;
}
.skill-switch-slider {
  position: absolute;
  inset: 0;
  border-radius: 10px;
  background: #d1d5db;
  transition: background 150ms ease;
  pointer-events: none;
}
.skill-switch-slider::before {
  content: '';
  position: absolute;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #ffffff;
  top: 2px;
  left: 2px;
  transition: transform 150ms ease;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2);
}
.skill-switch input:checked + .skill-switch-slider {
  background: #10a37f;
}
.skill-switch input:checked + .skill-switch-slider::before {
  transform: translateX(16px);
}
.skill-switch input:disabled + .skill-switch-slider {
  opacity: 0.5;
}
</style>
