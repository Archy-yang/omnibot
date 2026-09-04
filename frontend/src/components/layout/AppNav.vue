<script setup lang="ts">
/**
 * AppNav — v2.0 顶部导航条(52px)
 *
 * 左侧 brand,右侧两个文字+图标按钮。「记忆」「设置」都通过 emit 触发父组件
 * 打开对应的右侧抽屉(MemoryDrawer / SettingsDrawer)——v2.0 设计稿统一交互:
 * 主对话页面常驻,抽屉从右侧滑入,不切路由不丢上下文。
 *
 * 视觉照搬 docs/60-设计/omnibot-prototype/pages/v2-home-empty.html 的 .top-nav 段。
 */
defineProps<{
  /** 当前哪个抽屉打开:用于高亮对应按钮(可选) */
  current?: 'chat' | 'memory' | 'skills' | 'settings';
}>();

defineEmits<{
  /** 点击「记忆」:打开记忆抽屉 */
  'open-memory': [];
  /** 点击「技能」:打开技能抽屉(13-插件系统,MCP 接入 + 技能启停) */
  'open-skills': [];
  /** 点击「设置」:打开设置抽屉 */
  'open-settings': [];
}>();
</script>

<template>
  <nav class="top-nav">
    <!-- 左:品牌标识 -->
    <div class="brand">
      <span class="brand-logo">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M12 2C6.48 2 2 5.58 2 10c0 2.06 1.06 3.92 2.75 5.26L4 18l3.08-1.54C8.62 16.82 10.28 17 12 17c5.52 0 10-3.58 10-8s-4.48-7-10-7z" fill="#ffffff"/>
          <circle cx="9" cy="9" r="1.5" fill="#ffffff"/>
          <circle cx="15" cy="9" r="1.5" fill="#ffffff"/>
          <line x1="10.5" y1="9" x2="13.5" y2="9" stroke="#ffffff" stroke-width="1"/>
        </svg>
      </span>
      <span class="brand-name">OmniBot</span>
    </div>

    <!-- 右:导航按钮 -->
    <div class="nav-actions">
      <button
        class="nav-btn"
        :class="{ 'is-active': current === 'memory' }"
        type="button"
        @click="$emit('open-memory')"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M9.5 2A2.5 2.5 0 0 1 12 4.5v15a2 2 0 0 1-2-2V6.5a.5.5 0 0 0-.5-.5H4a2 2 0 0 1 0-4h5.5z"/>
          <path d="M14.5 2A2.5 2.5 0 0 0 12 4.5v15a2 2 0 0 0 2-2V6.5a.5.5 0 0 1 .5-.5H20a2 2 0 0 0 0-4h-5.5z"/>
        </svg>
        <span>记忆</span>
      </button>
      <button
        class="nav-btn"
        :class="{ 'is-active': current === 'skills' }"
        type="button"
        @click="$emit('open-skills')"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>
        </svg>
        <span>技能</span>
      </button>
      <button
        class="nav-btn"
        :class="{ 'is-active': current === 'settings' }"
        type="button"
        @click="$emit('open-settings')"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1.08-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1.08 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9c.26.604.852.997 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1.08z"/>
        </svg>
        <span>设置</span>
      </button>
    </div>
  </nav>
</template>

<style scoped>
.top-nav {
  height: 52px;
  min-height: 52px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
  background: #ffffff;
  border-bottom: 1px solid #f0f0f0;
  flex-shrink: 0;
}

/* 左侧品牌 */
.brand {
  display: flex;
  align-items: center;
  gap: 8px;
}

.brand-logo {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #10a37f;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.brand-name {
  font-size: 15px;
  font-weight: 600;
  color: #171717;
  line-height: 1;
}

/* 右侧按钮组 */
.nav-actions {
  display: flex;
  align-items: center;
  gap: 24px;
}

.nav-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 14px;
  color: #666666;
  font-family: inherit;
  padding: 4px 0;
  transition: color 150ms ease;
}

.nav-btn:hover {
  color: #171717;
}

/* 当前页对应按钮高亮 */
.nav-btn.is-active {
  color: #171717;
  font-weight: 500;
}

.nav-btn svg {
  flex-shrink: 0;
}
</style>
