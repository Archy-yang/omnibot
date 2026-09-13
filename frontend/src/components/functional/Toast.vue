<script setup lang="ts">
import type { ToastProps } from '@/types/components';

// 图标配色走 CSS 类（亮暗两套令牌），不再由 JS 返回写死的 hex
defineProps<ToastProps>();
</script>

<template>
  <Teleport to="body">
    <div class="toast-container">
      <TransitionGroup name="toast">
        <div
          v-for="toast in toasts"
          :key="toast.id"
          class="toast-item"
        >
          <!-- Icon -->
          <div class="toast-icon" :class="`toast-icon--${toast.type}`">
            <svg v-if="toast.type === 'success'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
            <svg v-else-if="toast.type === 'error'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="10"/>
              <line x1="15" y1="9" x2="9" y2="15"/>
              <line x1="9" y1="9" x2="15" y2="15"/>
            </svg>
            <svg v-else-if="toast.type === 'warning'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
              <line x1="12" y1="9" x2="12" y2="13"/>
              <line x1="12" y1="17" x2="12.01" y2="17"/>
            </svg>
            <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="10"/>
              <line x1="12" y1="16" x2="12" y2="12"/>
              <line x1="12" y1="8" x2="12.01" y2="8"/>
            </svg>
          </div>

          <!-- Message -->
          <span class="toast-message">{{ toast.message }}</span>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.toast-container {
  position: fixed;
  top: 40px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 9999;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  pointer-events: none;
}

.toast-item {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  /* dsh Toast：反色胶囊（亮色下深底、暗色下浅底），无边框 */
  background: var(--btn-primary-fill);
  color: var(--btn-primary-foreground);
  border-radius: 14px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18), 0 2px 4px rgba(0, 0, 0, 0.06);
  pointer-events: auto;
  width: max-content;
  max-width: min(640px, calc(100vw - 48px));
}

.toast-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

/* 图标语义色：深底上用亮阶，浅底（暗色模式反色胶囊）上用深阶 */
.toast-icon--success { color: var(--ds-green-400); }
.toast-icon--error { color: var(--ds-red-400); }
.toast-icon--warning { color: var(--ds-amber-400); }
.toast-icon--info { color: var(--ds-blue-400); }

:global(.dark) .toast-icon--success { color: var(--ds-green-500); }
:global(.dark) .toast-icon--error { color: var(--ds-red-600); }
:global(.dark) .toast-icon--warning { color: var(--ds-amber-500); }
:global(.dark) .toast-icon--info { color: var(--ds-blue-500); }

.toast-message {
  font-size: 14px;
  font-weight: 500;
  color: inherit;
  line-height: 1.6;
  white-space: nowrap;
}

/* Transitions */
.toast-enter-from {
  opacity: 0;
  transform: translateY(-12px);
}

.toast-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}

.toast-enter-active,
.toast-leave-active {
  transition: opacity 0.25s ease, transform 0.25s ease;
}

.toast-move {
  transition: transform 0.25s ease;
}
</style>
