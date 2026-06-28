<script setup lang="ts">
/**
 * DrawerShell — v2.0 右侧抽屉通用壳
 *
 * 设计稿(docs/60-设计/omnibot-prototype/pages/v2-memory.html /
 * v2-settings.html)统一交互:点击 AppNav 的「记忆」/「设置」从右侧滑入
 * 400px 抽屉,主对话页面常驻背景。MemoryDrawer / SettingsDrawer 共用这一壳层,
 * 避免重复实现遮罩/滑入动画/ESC 关闭/标题栏。
 *
 * 行为:
 *   - 点遮罩 / 点 X / 按 ESC → emit 'close'
 *   - visible 时禁用 body 滚动,关闭后恢复
 *   - Teleport 到 body,避免被父容器 overflow:hidden 裁切
 */
import { watch, onBeforeUnmount } from 'vue';

const props = defineProps<{
  visible: boolean;
  title: string;
}>();

const emit = defineEmits<{
  close: [];
}>();

// ESC 监听:visible 时挂载,关闭时立即移除(避免事件累积)
const handleKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Escape') emit('close');
};

// visible 切换时控制 body 滚动 + ESC 监听
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

// 组件销毁时清理(避免页面切换时 body 锁死)
onBeforeUnmount(() => {
  document.body.style.overflow = '';
  window.removeEventListener('keydown', handleKeydown);
});
</script>

<template>
  <Teleport to="body">
    <Transition name="drawer">
      <div v-if="visible" class="drawer-root">
        <!-- 遮罩:点击关闭 -->
        <div class="drawer-overlay" @click="emit('close')"></div>

        <!-- 抽屉:右侧 400px -->
        <aside
          class="drawer"
          role="dialog"
          :aria-label="title"
          @click.stop
        >
          <header class="drawer-header">
            <span class="drawer-title">{{ title }}</span>
            <button
              type="button"
              class="drawer-close"
              :aria-label="`关闭${title}`"
              @click="emit('close')"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </header>

          <div class="drawer-body">
            <slot />
          </div>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.drawer-root {
  position: fixed;
  inset: 0;
  z-index: 100;
  pointer-events: auto;
}

.drawer-overlay {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.2);
  animation: drawer-fade-in 150ms ease forwards;
}

.drawer {
  position: absolute;
  top: 0;
  right: 0;
  width: 400px;
  max-width: 100vw;
  height: 100vh;
  background: #ffffff;
  border-left: 1px solid #f0f0f0;
  box-shadow: -4px 0 24px rgba(0, 0, 0, 0.06);
  display: flex;
  flex-direction: column;
  animation: drawer-slide-in 200ms ease forwards;
}

.drawer-header {
  padding: 20px 24px 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-shrink: 0;
}

.drawer-title {
  font-size: 18px;
  font-weight: 600;
  color: #171717;
}

.drawer-close {
  width: 28px;
  height: 28px;
  border-radius: 6px;
  border: none;
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: #999;
  transition: background 150ms ease, color 150ms ease;
}

.drawer-close:hover {
  background: #f5f5f5;
  color: #666;
}

.drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 0 24px 32px;
}

/* 滚动条美化:4px 细线条 */
.drawer-body::-webkit-scrollbar {
  width: 4px;
}
.drawer-body::-webkit-scrollbar-track {
  background: transparent;
}
.drawer-body::-webkit-scrollbar-thumb {
  background: #e0e0e0;
  border-radius: 2px;
}
.drawer-body::-webkit-scrollbar-thumb:hover {
  background: #ccc;
}

@keyframes drawer-fade-in {
  from { opacity: 0; }
  to { opacity: 1; }
}

@keyframes drawer-slide-in {
  from { transform: translateX(100%); }
  to { transform: translateX(0); }
}

/* 关闭时的反向动画(Transition leave) */
.drawer-leave-active .drawer-overlay {
  animation: drawer-fade-in 150ms ease reverse forwards;
}
.drawer-leave-active .drawer {
  animation: drawer-slide-in 200ms ease reverse forwards;
}
</style>
