<script setup lang="ts">
/**
 * Sidebar — dsh (DeepSeek Harness) 风格左侧栏
 *
 * 布局几何移植自 dsh ui-sidebar SidebarRoot.module.css：
 * - 列内边距 6/12px，底色 sidebar-fill，右侧 0.5px 细线（由本组件自带）
 * - 品牌行：字标 17px/600 + 右侧 28px 圆形折叠按钮
 * - 导航行：min-height 36px、r8、hover 微灰、选中底加深 + 加粗
 * - 可折叠：56px rail，内容 150ms 淡出后列宽收窄
 *
 * 产品基线是单一长期对话，无「新对话」概念，故无会话列表；
 * 导航四项：对话（关闭全部抽屉）/ 记忆 / 技能 / 设置（打开右侧抽屉）。
 * 底部固定：主题切换（settingsStore.toggleTheme）。
 */
import { ref } from 'vue';
import { useSettingsStore } from '@/stores/settings';

const props = defineProps<{
  /** 当前高亮的导航项：弹窗打开时对应高亮，都没开时为 chat */
  current?: 'chat' | 'memory' | 'subscriptions' | 'skills' | 'settings';
}>();

const emit = defineEmits<{
  'open-chat': [];
  'open-memory': [];
  'open-subscriptions': [];
  'open-skills': [];
  'open-settings': [];
}>();

const settingsStore = useSettingsStore();

// 折叠态仅存本组件（刷新复位），不进 store——纯 UI 偏好，不值得持久化
const collapsed = ref(false);

const navItems = [
  { key: 'chat', label: '对话' },
  { key: 'memory', label: '记忆' },
  { key: 'subscriptions', label: '订阅' },
  { key: 'skills', label: '技能' },
  { key: 'settings', label: '设置' },
] as const;

const handleNav = (key: 'chat' | 'memory' | 'subscriptions' | 'skills' | 'settings') => {
  switch (key) {
    case 'chat': emit('open-chat'); break;
    case 'memory': emit('open-memory'); break;
    case 'subscriptions': emit('open-subscriptions'); break;
    case 'skills': emit('open-skills'); break;
    case 'settings': emit('open-settings'); break;
  }
};
</script>

<template>
  <aside class="sidebar" :class="{ collapsed }">
    <!-- 品牌行：字标 + 折叠按钮 -->
    <div class="logo-row">
      <button v-show="!collapsed" type="button" class="brand" @click="emit('open-chat')">
        <span class="brand-mark" aria-hidden="true">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none">
            <path d="M12 2C6.48 2 2 5.58 2 10c0 2.06 1.06 3.92 2.75 5.26L4 18l3.08-1.54C8.62 16.82 10.28 17 12 17c5.52 0 10-3.58 10-8s-4.48-7-10-7z" fill="currentColor"/>
            <circle cx="9" cy="9" r="1.5" fill="var(--bg-base)"/>
            <circle cx="15" cy="9" r="1.5" fill="var(--bg-base)"/>
            <line x1="10.5" y1="9" x2="13.5" y2="9" stroke="var(--bg-base)" stroke-width="1"/>
          </svg>
        </span>
        <span class="brand-name">OmniBot</span>
      </button>
      <button
        type="button"
        class="icon-btn"
        :aria-label="collapsed ? '展开侧栏' : '收起侧栏'"
        :title="collapsed ? '展开侧栏' : '收起侧栏'"
        @click="collapsed = !collapsed"
      >
        <!-- panel-left 图标；收起态镜像为 panel-right -->
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" :style="collapsed ? 'transform: scaleX(-1)' : ''">
          <rect width="18" height="18" x="3" y="3" rx="2"/>
          <path d="M9 3v18"/>
        </svg>
      </button>
    </div>

    <!-- 导航行 -->
    <nav class="nav-list">
      <button
        v-for="item in navItems"
        :key="item.key"
        type="button"
        class="nav-row"
        :class="{ 'is-active': props.current === item.key }"
        :title="collapsed ? item.label : undefined"
        @click="handleNav(item.key)"
      >
        <span class="nav-glyph" aria-hidden="true">
          <!-- 对话 -->
          <svg v-if="item.key === 'chat'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
          </svg>
          <!-- 记忆 -->
          <svg v-else-if="item.key === 'memory'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M9.5 2A2.5 2.5 0 0 1 12 4.5v15a2 2 0 0 1-2-2V6.5a.5.5 0 0 0-.5-.5H4a2 2 0 0 1 0-4h5.5z"/>
            <path d="M14.5 2A2.5 2.5 0 0 0 12 4.5v15a2 2 0 0 0 2-2V6.5a.5.5 0 0 1 .5-.5H20a2 2 0 0 0 0-4h-5.5z"/>
          </svg>
          <!-- 订阅(RSS) -->
          <svg v-else-if="item.key === 'subscriptions'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 11a9 9 0 0 1 9 9"/>
            <path d="M4 4a16 16 0 0 1 16 16"/>
            <circle cx="5" cy="19" r="1"/>
          </svg>
          <!-- 技能 -->
          <svg v-else-if="item.key === 'skills'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>
          </svg>
          <!-- 设置 -->
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="3"/>
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1.08-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1.08 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9c.26.604.852.997 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1.08z"/>
          </svg>
        </span>
        <span v-show="!collapsed" class="nav-label">{{ item.label }}</span>
      </button>
    </nav>

    <!-- 底部固定：主题切换 -->
    <div class="foot">
      <button
        type="button"
        class="nav-row theme-row"
        :title="collapsed ? (settingsStore.theme === 'dark' ? '切换到亮色' : '切换到暗色') : undefined"
        @click="settingsStore.toggleTheme()"
      >
        <span class="nav-glyph" aria-hidden="true">
          <!-- 亮色态显示月亮（点击切暗色），暗色态显示太阳 -->
          <svg v-if="settingsStore.theme !== 'dark'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9z"/>
          </svg>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="4"/>
            <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/>
          </svg>
        </span>
        <span v-show="!collapsed" class="nav-label">
          {{ settingsStore.theme === 'dark' ? '亮色模式' : '暗色模式' }}
        </span>
      </button>
    </div>
  </aside>
</template>

<style scoped>
.sidebar {
  display: flex;
  flex-direction: column;
  width: 240px;
  flex-shrink: 0;
  height: 100%;
  padding: 6px 12px;
  box-sizing: border-box;
  background: var(--bg-sidebar);
  border-right: 0.5px solid var(--border-l3);
  overflow: hidden;
  transition: width 200ms ease;
}

.sidebar.collapsed {
  width: 56px;
  padding: 6px 10px;
}

/* ===== 品牌行（dsh logoRow：h60，字标左、折叠钮右） ===== */
.logo-row {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  height: 52px;
  margin-bottom: 8px;
}

.sidebar.collapsed .logo-row {
  height: 36px;
  justify-content: flex-start;
  margin-bottom: 12px;
}

.brand {
  flex: 1;
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 24px;
  padding: 0;
  border: none;
  background: transparent;
  cursor: pointer;
}

.brand-mark {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--accent);
}

.brand-name {
  font-size: 17px;
  font-weight: 600;
  line-height: 24px;
  letter-spacing: 0.04em;
  color: var(--label-primary);
  white-space: nowrap;
}

.icon-btn {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 50%;
  padding: 0;
  background: transparent;
  cursor: pointer;
  color: var(--label-secondary);
  transition: background 150ms ease;
}

.icon-btn:hover {
  background: var(--bg-hover);
}

.sidebar.collapsed .icon-btn {
  width: 36px;
  height: 36px;
  color: var(--label-primary);
}

/* ===== 导航行（dsh panelRow：min-height 36、r8、glyph+label） ===== */
.nav-list {
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.nav-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 36px;
  padding: 7px 8px;
  box-sizing: border-box;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--label-secondary);
  font: inherit;
  font-size: 14px;
  line-height: 22px;
  text-align: left;
  cursor: pointer;
  white-space: nowrap;
  transition: background 150ms ease;
}

.nav-row:hover {
  background: var(--bg-hover);
}

.nav-row.is-active {
  background: var(--bg-active);
  color: var(--label-primary);
  font-weight: 500;
}

.sidebar.collapsed .nav-row {
  width: 36px;
  min-height: 36px;
  padding: 0;
  justify-content: center;
  color: var(--label-primary);
}

.sidebar.collapsed .nav-row.is-active {
  color: var(--accent);
}

.nav-glyph {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.nav-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* ===== 底部固定区（dsh footArea） ===== */
.foot {
  flex: none;
  margin-top: auto;
  display: flex;
  flex-direction: column;
}

.sidebar.collapsed .foot {
  align-items: center;
}

.sidebar.collapsed .foot .nav-row {
  width: 36px;
}
</style>
