<script setup lang="ts">
/**
 * DialogShell — dsh (DeepSeek Harness) 风格居中弹窗壳
 *
 * 几何移植自 dsh 两处弹窗实现：
 * - 小弹窗（ui-primitives Modal.module.css）：r24、遮罩 24% 黑 + 2px 模糊、
 *   header（22/14/12/24）内标题 16/500 + 右侧 28px 关闭钮、内容列 24px 边距
 * - 大设置弹窗（ui-settings-general SettingsRoot.module.css）：800 宽 ×
 *   min(800, 100vh-48) 高、r32、左侧 188px 分类导航（导航格 40 高 r12，
 *   选中填充 bluish-100）+ 右侧内容滚动；面板高度固定不随分类切换抖动
 *
 * 行为：点遮罩 / 点 X / 按 ESC → emit 'close'；visible 时锁 body 滚动。
 * 无 navItems 时为窄弹窗（标题在顶部 header）；有 navItems 时为宽弹窗
 * （标题进导航列，内容区随 activeNav 切换由调用方控制）。
 */
import { watch, onBeforeUnmount } from 'vue';

const props = defineProps<{
  visible: boolean;
  title: string;
  /** 面板宽度：无导航默认 560px；设置类导航布局传 800px */
  width?: string;
  /** 分类导航项：提供即启用左侧导航布局 */
  navItems?: readonly { key: string; label: string }[];
  /** 当前选中分类（配合 navItems 使用） */
  activeNav?: string;
}>();

const emit = defineEmits<{
  close: [];
  'update:activeNav': [key: string];
}>();

// ESC 关闭:visible 时挂载,关闭时立即移除(避免事件累积)
const handleKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Escape') emit('close');
};

watch(
  () => props.visible,
  (v) => {
    if (v) {
      document.body.style.overflow = 'hidden';
      window.addEventListener('keydown', handleKeydown);
    } else {
      document.body.style.overflow = '';
      window.removeEventListener('keydown', handleKeydown);
    }
  },
  { immediate: true }
);

onBeforeUnmount(() => {
  document.body.style.overflow = '';
  window.removeEventListener('keydown', handleKeydown);
});
</script>

<template>
  <Teleport to="body">
    <Transition name="dialog">
      <div v-if="visible" class="dialog-root">
        <!-- 遮罩:点击关闭(24% 黑 + 2px 模糊) -->
        <div class="dialog-mask" @click="emit('close')"></div>

        <div
          class="dialog-panel"
          :class="{ 'has-nav': navItems && navItems.length > 0 }"
          :style="{ width: width ?? '560px' }"
          role="dialog"
          :aria-label="title"
        >
          <button
            type="button"
            class="dialog-close"
            :aria-label="`关闭${title}`"
            @click="emit('close')"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <line x1="18" y1="6" x2="6" y2="18"/>
              <line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>

          <!-- 左侧分类导航布局(设置类大弹窗) -->
          <template v-if="navItems && navItems.length > 0">
            <div class="dialog-nav">
              <div class="dialog-nav-title">{{ title }}</div>
              <div class="dialog-nav-list">
                <button
                  v-for="item in navItems"
                  :key="item.key"
                  type="button"
                  class="dialog-nav-cell"
                  :class="{ 'is-active': activeNav === item.key }"
                  @click="emit('update:activeNav', item.key)"
                >
                  {{ item.label }}
                </button>
              </div>
            </div>
            <div class="dialog-content">
              <slot />
            </div>
          </template>

          <!-- 常规布局:顶部标题 + 内容 -->
          <template v-else>
            <div class="dialog-header">
              <span class="dialog-title">{{ title }}</span>
            </div>
            <div class="dialog-body">
              <slot />
            </div>
          </template>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.dialog-root {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.dialog-mask {
  position: absolute;
  inset: 0;
  background: var(--bg-mask);
  backdrop-filter: blur(2px);
}

.dialog-panel {
  position: relative;
  z-index: 1;
  display: flex;
  flex-direction: column;
  max-width: calc(100vw - 48px);
  max-height: calc(100vh - 48px);
  border-radius: 24px;
  overflow: hidden;
  background: var(--bg-layer-2);
  box-shadow: 0 16px 48px rgba(0, 0, 0, 0.16), 0 4px 12px rgba(0, 0, 0, 0.08);
}

/* 导航布局:800 高面板固定高度,分类切换不抖动(dsh SettingsRoot) */
.dialog-panel.has-nav {
  flex-direction: row;
  width: 800px !important;
  height: min(800px, calc(100vh - 48px));
  border-radius: 32px;
}

/* ===== 关闭钮:导航布局悬浮右上,常规布局在 header 行内 ===== */
.dialog-close {
  position: absolute;
  top: 14px;
  right: 14px;
  z-index: 2;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 8px;
  background: transparent;
  cursor: pointer;
  color: var(--label-secondary);
  transition: background 150ms ease;
}

.dialog-close:hover {
  background: var(--bg-hover);
}

/* ===== 常规布局 ===== */
.dialog-header {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 22px 24px 12px;
}

.dialog-title {
  font-size: 16px;
  line-height: 24px;
  font-weight: 500;
  color: var(--label-primary);
}

.dialog-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 0 24px 24px;
}

/* ===== 左侧分类导航(dsh .Setting-nav:188 宽,格 40 高 r12) ===== */
.dialog-nav {
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 18px;
  width: 188px;
  padding: 22px 12px 0;
  box-sizing: border-box;
}

.dialog-nav-title {
  padding: 0 12px;
  font-size: 16px;
  line-height: 24px;
  font-weight: 500;
  color: var(--label-primary);
}

.dialog-nav-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.dialog-nav-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 40px;
  padding: 9px 16px 9px 12px;
  box-sizing: border-box;
  border: none;
  border-radius: 12px;
  background: transparent;
  cursor: pointer;
  font-family: inherit;
  font-size: 14px;
  line-height: 22px;
  font-weight: 400;
  color: var(--label-primary);
  text-align: left;
  transition: background 150ms ease;
}

.dialog-nav-cell:hover {
  background: var(--bg-hover);
}

.dialog-nav-cell.is-active {
  background: var(--sidebar-active);
  font-weight: 500;
}

/* ===== 导航布局右侧内容区 ===== */
.dialog-content {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 24px 24px 24px;
}

/* ===== 进出场性能要点 =====
   不在 .dialog-root 整层上做 opacity 动画——整层容器内含 backdrop-filter
   模糊层,对它做透明度动画会迫使 Chrome 每帧重算背景模糊(卡顿根源)。
   改为拆开:遮罩只动自己的 opacity(模糊纹理可缓存),面板只动
   transform+opacity(合成器路径),各自动画各自提升图层。 */
.dialog-enter-active .dialog-mask {
  transition: opacity 160ms ease;
}

.dialog-leave-active .dialog-mask {
  transition: opacity 140ms ease;
}

.dialog-enter-from .dialog-mask,
.dialog-leave-to .dialog-mask {
  opacity: 0;
}

.dialog-enter-active .dialog-panel {
  transition: transform 160ms cubic-bezier(0.2, 0.8, 0.3, 1), opacity 160ms ease;
  will-change: transform; /* 仅 enter 期间存在,类移除即释放 */
}

.dialog-leave-active .dialog-panel {
  transition: transform 140ms ease, opacity 140ms ease;
}

.dialog-enter-from .dialog-panel {
  transform: scale(0.96);
  opacity: 0;
}

.dialog-leave-to .dialog-panel {
  transform: scale(0.98);
  opacity: 0;
}

@media (prefers-reduced-motion: reduce) {
  .dialog-enter-active .dialog-mask,
  .dialog-leave-active .dialog-mask,
  .dialog-enter-active .dialog-panel,
  .dialog-leave-active .dialog-panel {
    transition: none;
  }
}
</style>
