<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue';
import { useChat, useSettings, useToast } from '@/composables';
import AppNav from '@/components/layout/AppNav.vue';
import ChatMessageList from '@/components/chat/ChatMessageList.vue';
import ChatInput from '@/components/chat/ChatInput.vue';
import ChatAvatar from '@/components/chat/ChatAvatar.vue';
import SettingsDrawer from '@/components/functional/SettingsDrawer.vue';
import MemoryDrawer from '@/components/functional/MemoryDrawer.vue';
import SkillDrawer from '@/components/functional/SkillDrawer.vue';
import Toast from '@/components/functional/Toast.vue';
import type { Message } from '@/types/chat';

// v2.1: 身份由后端 JWT 中间件解析,前端不再维护 sessionId(单一长期对话模型)
const { messages, isLoading, sendMessage, loadHistory, startPollingUnreported, stopPollingUnreported } = useChat();
const { showSettingsPanel, toggleSettingsPanel, loadConfig } = useSettings();
const { toasts, error } = useToast();

const inputValue = ref('');
const isInitializing = ref(true);

// 记忆/技能抽屉本地状态(设置抽屉走 settingsStore.showSettingsPanel)
const showMemoryDrawer = ref(false);
const showSkillDrawer = ref(false);

const isEmpty = computed(() => messages.value.length === 0);
const inputPlaceholder = computed(() =>
  isEmpty.value ? '有什么可以帮你的？' : '继续对话...'
);

// AppNav 高亮:抽屉打开时高亮对应按钮,都没开时高亮 chat
const navCurrent = computed<'chat' | 'memory' | 'skills' | 'settings'>(() => {
  if (showMemoryDrawer.value) return 'memory';
  if (showSkillDrawer.value) return 'skills';
  if (showSettingsPanel.value) return 'settings';
  return 'chat';
});

onMounted(async () => {
  try {
    await loadConfig();
    await loadHistory();
    // 启动后台 Agent 任务轮询(08 §4.5):发现完成的子任务自动在对话框汇报
    startPollingUnreported();
  } catch (err) {
    console.error('Init failed:', err);
    error('初始化失败，请刷新页面重试');
  } finally {
    isInitializing.value = false;
  }
});

onUnmounted(() => {
  stopPollingUnreported();
});

const handleSend = async (content: string) => {
  if (!content.trim()) return;
  try {
    await sendMessage(content);
  } catch (err) {
    error('消息发送失败，请重试');
  }
};
</script>

<template>
  <div class="chat-layout">
    <AppNav
      :current="navCurrent"
      @open-memory="showMemoryDrawer = true"
      @open-skills="showSkillDrawer = true"
      @open-settings="toggleSettingsPanel"
    />

    <main class="main">
      <!-- 空状态:居中欢迎区 + 输入框 -->
      <div v-if="isEmpty && !isLoading && !isInitializing" class="welcome-area">
        <div class="welcome-icon">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <path d="M12 2C6.48 2 2 5.58 2 10c0 2.06 1.06 3.92 2.75 5.26L4 18l3.08-1.54C8.62 16.82 10.28 17 12 17c5.52 0 10-3.58 10-8s-4.48-7-10-7z" fill="#ffffff"/>
            <circle cx="9" cy="9" r="1.5" fill="#ffffff"/>
            <circle cx="15" cy="9" r="1.5" fill="#ffffff"/>
            <line x1="10.5" y1="9" x2="13.5" y2="9" stroke="#ffffff" stroke-width="1"/>
          </svg>
        </div>
        <h1 class="welcome-title">你好，我是 OmniBot</h1>
        <p class="welcome-subtitle">你的私人助理，写代码、查资料、聊天，随时告诉我</p>

        <div class="welcome-input">
          <ChatInput
            v-model="inputValue"
            :placeholder="inputPlaceholder"
            @send="handleSend"
          />
        </div>
      </div>

      <!-- 有消息:列表 + 底部输入框 -->
      <template v-else>
        <ChatMessageList
          :messages="messages as Message[]"
          :is-loading="isInitializing || isLoading"
        >
          <template #avatar="{ message }">
            <ChatAvatar :role="message.role === 'user' ? 'user' : 'assistant'" />
          </template>
        </ChatMessageList>

        <div class="bottom-input">
          <ChatInput
            v-model="inputValue"
            :placeholder="inputPlaceholder"
            @send="handleSend"
          />
        </div>
      </template>
    </main>

    <SettingsDrawer
      :visible="showSettingsPanel"
      @close="toggleSettingsPanel"
      @update-config="() => {}"
    />

    <MemoryDrawer
      :visible="showMemoryDrawer"
      @close="showMemoryDrawer = false"
    />

    <SkillDrawer
      :visible="showSkillDrawer"
      @close="showSkillDrawer = false"
    />

    <Toast :toasts="toasts" />
  </div>
</template>

<style scoped>
.chat-layout {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: #ffffff;
}

.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  position: relative;
}

/* ===== 空状态欢迎区(居中) ===== */
.welcome-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 0 24px 48px;
  gap: 16px;
}

.welcome-icon {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: #10a37f;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-bottom: 4px;
}

.welcome-title {
  font-size: 22px;
  font-weight: 600;
  color: #171717;
  line-height: 1.3;
  margin: 0;
}

.welcome-subtitle {
  font-size: 14px;
  color: #999999;
  line-height: 1.4;
  margin: 0 0 16px;
  text-align: center;
}

.welcome-input {
  width: 100%;
  max-width: 680px;
}

/* ===== 有消息:底部固定输入区 ===== */
.bottom-input {
  flex-shrink: 0;
  padding: 8px 0 16px;
}
</style>
