<script setup lang="ts">
/**
 * MemoryPage — v2.0 记忆独立页面
 *
 * 路由 /memory。顶部 AppNav,主体复用 MemorySection 组件(原 SettingsPanel 内的
 * memory tab 内容)。SettingsPanel 仍可从顶部 nav 唤起,实现 v2.0「记忆是一等公民」
 * 的同时,不破坏现有设置入口。
 */
import { useSettings, useToast } from '@/composables';
import AppNav from '@/components/layout/AppNav.vue';
import MemorySection from '@/components/functional/MemorySection.vue';
import SettingsPanel from '@/components/functional/SettingsPanel.vue';
import Toast from '@/components/functional/Toast.vue';

const { showSettingsPanel, toggleSettingsPanel } = useSettings();
const { toasts, success } = useToast();
</script>

<template>
  <div class="memory-layout">
    <AppNav current="memory" @open-settings="toggleSettingsPanel" />

    <main class="memory-main">
      <div class="memory-container">
        <header class="memory-header">
          <h1 class="memory-title">长期记忆</h1>
          <p class="memory-subtitle">
            这些记忆会在每次对话中自动注入,帮助助手更了解你。请不要保存密码、API Key、身份证号等敏感信息。
          </p>
        </header>

        <MemorySection />
      </div>
    </main>

    <SettingsPanel
      :visible="showSettingsPanel"
      @close="toggleSettingsPanel"
      @update-config="() => { success('配置已保存') }"
    />

    <Toast :toasts="toasts" />
  </div>
</template>

<style scoped>
.memory-layout {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: #ffffff;
}

.memory-main {
  flex: 1;
  overflow-y: auto;
  padding: 32px 24px 48px;
}

.memory-container {
  max-width: 768px;
  margin: 0 auto;
}

.memory-header {
  margin-bottom: 24px;
}

.memory-title {
  font-size: 22px;
  font-weight: 600;
  color: #0d0d0d;
  margin-bottom: 8px;
  line-height: 1.3;
}

.memory-subtitle {
  font-size: 13px;
  color: #6e6e80;
  line-height: 1.6;
}
</style>
