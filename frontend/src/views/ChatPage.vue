<script setup lang="ts">
import { ref, onMounted, watch, computed } from 'vue';
import { useChat, useSession, useSettings, useToast } from '@/composables';
import ChatMessageList from '@/components/chat/ChatMessageList.vue';
import ChatInput from '@/components/chat/ChatInput.vue';
import ChatAvatar from '@/components/chat/ChatAvatar.vue';
import SettingsPanel from '@/components/functional/SettingsPanel.vue';
import Toast from '@/components/functional/Toast.vue';
import type { Message } from '@/types/chat';

const { messages, isLoading, sendMessage, loadHistory } = useChat();
const { initSession, sessionId, newSession: createNewSession } = useSession();
const { showSettingsPanel, toggleSettingsPanel, loadConfig } = useSettings();
const { toasts, success, error } = useToast();

const inputValue = ref('');
const isInitializing = ref(true);
const sidebarOpen = ref(true);

const conversations = ref([
  { id: '1', title: 'Feishu 插件重复问题' },
  { id: '2', title: 'OpenClaw在中国应用' },
  { id: '3', title: 'macOS 系统数据解释' },
  { id: '4', title: 'Go版本升级方法' },
  { id: '5', title: 'Claude 配置 DeepSeek' },
]);

const isEmpty = computed(() => messages.value.length === 0);

onMounted(async () => {
  try {
    initSession();
    await loadConfig();
    await loadHistory();
  } catch (err) {
    console.error('Init failed:', err);
    error('初始化失败，请刷新页面重试');
  } finally {
    isInitializing.value = false;
  }
});

watch(() => sessionId.value, () => {
  loadHistory().catch(() => {});
});

const handleSend = async (content: string, isAgentMode: boolean) => {
  if (!content.trim()) return;
  try {
    await sendMessage(content, isAgentMode);
  } catch (err) {
    error('消息发送失败，请重试');
  }
};

const handleNewChat = () => {
  createNewSession();
  success('已创建新对话');
};
</script>

<template>
  <div class="chat-layout">
    <!-- Sidebar (full) -->
    <aside v-if="sidebarOpen" class="sidebar">
      <!-- Top: Logo + Toggle -->
      <div class="sidebar-top">
        <button class="logo-btn" @click="handleNewChat">
          <svg width="24" height="24" viewBox="0 0 41 41" fill="#10a37f">
            <path d="M37.5324 16.8707c.9886-2.9594.6594-6.2024-.9027-8.8945-2.348-4.0728-7.0494-6.1685-11.62-5.1791C22.6324 1.0184 19.7864.0035 16.9043.0035c-4.7113 0-8.8888 3.0182-10.3375 7.4773C3.4754 8.4661 1.0148 10.7402.0093 13.7066c-2.354 4.0712-2.1146 9.2009.5901 12.9772-.9886 2.9595-.6594 6.2024.9027 8.8946 2.348 4.0727 7.0494 6.1684 11.62 5.179 2.3792 2.5777 5.226 3.5926 8.1081 3.5926 4.7113 0 8.8888-3.0181 10.3375-7.4773 2.9508-.9852 5.4114-3.2594 6.4169-6.2258 2.3539-4.0712 2.1145-9.2009-.5902-12.9772z"/>
          </svg>
        </button>
        <button class="icon-btn" @click="sidebarOpen = false" title="收起侧边栏">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="3" width="18" height="18" rx="2"/>
            <line x1="9" y1="3" x2="9" y2="21"/>
          </svg>
        </button>
      </div>

      <!-- Menu -->
      <nav class="sidebar-menu">
        <button class="menu-item" @click="handleNewChat">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
            <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
          </svg>
          <span>新聊天</span>
        </button>
        <button class="menu-item">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="11" cy="11" r="8"/>
            <line x1="21" y1="21" x2="16.65" y2="16.65"/>
          </svg>
          <span>搜索聊天</span>
        </button>
        <button class="menu-item">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="3" width="18" height="18" rx="2"/>
            <circle cx="8.5" cy="8.5" r="1.5"/>
            <polyline points="21 15 16 10 5 21"/>
          </svg>
          <span>图片</span>
        </button>
        <button class="menu-item">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="3" width="7" height="7"/>
            <rect x="14" y="3" width="7" height="7"/>
            <rect x="14" y="14" width="7" height="7"/>
            <rect x="3" y="14" width="7" height="7"/>
          </svg>
          <span>应用</span>
        </button>
        <button class="menu-item" @click="toggleSettingsPanel">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="3"/>
            <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 01-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.32 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/>
          </svg>
          <span>设置</span>
        </button>
      </nav>

      <!-- Conversation history -->
      <div class="sidebar-history">
        <p class="history-title">你的聊天</p>
        <div class="history-list">
          <button
            v-for="conv in conversations"
            :key="conv.id"
            class="history-item"
          >
            {{ conv.title }}
          </button>
        </div>
      </div>

      <!-- Bottom: User -->
      <div class="sidebar-bottom">
        <button class="user-btn">
          <div class="user-avatar">U</div>
          <div class="user-info">
            <div class="user-name">用户</div>
            <div class="user-plan">免费版</div>
          </div>
        </button>
      </div>
    </aside>

    <!-- Sidebar (collapsed - icon only) -->
    <aside v-else class="sidebar-rail">
      <button class="rail-btn" @click="sidebarOpen = true" title="展开侧边栏">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="3" y="3" width="18" height="18" rx="2"/>
          <line x1="15" y1="3" x2="15" y2="21"/>
        </svg>
      </button>
      <button class="rail-btn" @click="handleNewChat" title="新聊天">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
        </svg>
      </button>
      <button class="rail-btn" title="搜索聊天">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="11" cy="11" r="8"/>
          <line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
      </button>
      <button class="rail-btn" title="图片">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="3" y="3" width="18" height="18" rx="2"/>
          <circle cx="8.5" cy="8.5" r="1.5"/>
          <polyline points="21 15 16 10 5 21"/>
        </svg>
      </button>

      <div class="rail-spacer"/>

      <button class="rail-btn" @click="toggleSettingsPanel" title="设置">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 01-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.32 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/>
        </svg>
      </button>
      <button class="rail-btn user-rail">
        <div class="rail-avatar">U</div>
      </button>
    </aside>

    <!-- Main area -->
    <main class="main">
      <!-- Top bar -->
      <header class="topbar">
        <div class="topbar-title">
          <span>OmniBot</span>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="6 9 12 15 18 9"/>
          </svg>
        </div>
        <div class="topbar-spacer"/>
        <button class="icon-btn">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
          </svg>
        </button>
      </header>

      <!-- Centered empty state -->
      <div v-if="isEmpty && !isLoading" class="centered-area">
        <h1 class="hero-title">准备好了，随时开始</h1>
        <div class="centered-input">
          <ChatInput
            v-model="inputValue"
            :disabled="isInitializing"
            :is-loading="isLoading"
            placeholder="有问题，尽管问"
            @send="handleSend"
          />
        </div>
      </div>

      <!-- Chat area when messages exist -->
      <template v-else>
        <ChatMessageList
          :messages="messages as Message[]"
          :is-loading="isInitializing || isLoading"
        >
          <template #avatar="{ message }">
            <ChatAvatar :role="message.role === 'user' ? 'user' : 'assistant'" />
          </template>
        </ChatMessageList>

        <ChatInput
          v-model="inputValue"
          :disabled="isInitializing"
          :is-loading="isLoading"
          placeholder="有问题，尽管问"
          @send="handleSend"
        />
      </template>

      <SettingsPanel
        :visible="showSettingsPanel"
        @close="toggleSettingsPanel"
        @update-config="() => { success('配置已保存') }"
      />

      <Toast :toasts="toasts" />
    </main>
  </div>
</template>

<style scoped>
.chat-layout {
  display: flex;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: #ffffff;
}

/* ================ Sidebar ================ */
.sidebar {
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  width: 260px;
  background: #f9f9f9;
  transition: width 0.2s ease;
  overflow: hidden;
}

.sidebar.collapsed {
  width: 0;
}

/* ================ Collapsed Rail ================ */
.sidebar-rail {
  display: flex;
  flex-direction: column;
  align-items: center;
  flex-shrink: 0;
  width: 52px;
  background: #ffffff;
  padding: 10px 0;
  gap: 4px;
}

.rail-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: none;
  background: transparent;
  border-radius: 8px;
  cursor: pointer;
  color: #5d5d6e;
  transition: background 0.15s;
}

.rail-btn:hover { background: rgba(0, 0, 0, 0.05); }

.rail-spacer { flex: 1; }

.user-rail {
  margin-top: 8px;
}

.rail-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: #5b9bd5;
  color: #ffffff;
  font-size: 12px;
  font-weight: 600;
}

.sidebar-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
}

.logo-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: none;
  background: transparent;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
}

.logo-btn:hover { background: rgba(0, 0, 0, 0.05); }

.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: none;
  background: transparent;
  border-radius: 8px;
  cursor: pointer;
  color: #5d5d6e;
  transition: background 0.15s;
}

.icon-btn:hover { background: rgba(0, 0, 0, 0.05); }

.sidebar-menu {
  display: flex;
  flex-direction: column;
  padding: 4px 8px;
  gap: 2px;
}

.menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border: none;
  background: transparent;
  border-radius: 8px;
  font-size: 14px;
  color: #0d0d0d;
  text-align: left;
  cursor: pointer;
  transition: background 0.15s;
}

.menu-item:hover { background: rgba(0, 0, 0, 0.05); }

.menu-item svg { flex-shrink: 0; color: #5d5d6e; }

.sidebar-history {
  flex: 1;
  overflow-y: auto;
  padding: 16px 8px 8px;
}

.history-title {
  padding: 4px 12px;
  font-size: 12px;
  font-weight: 500;
  color: #8e8ea0;
  margin-bottom: 4px;
}

.history-list {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.history-item {
  padding: 8px 12px;
  border: none;
  background: transparent;
  border-radius: 8px;
  font-size: 14px;
  color: #0d0d0d;
  text-align: left;
  cursor: pointer;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: background 0.15s;
}

.history-item:hover { background: rgba(0, 0, 0, 0.05); }

.sidebar-bottom {
  padding: 8px;
  border-top: 1px solid rgba(0, 0, 0, 0.05);
}

.user-btn {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 6px 8px;
  border: none;
  background: transparent;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
}

.user-btn:hover { background: rgba(0, 0, 0, 0.05); }

.user-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: #5b9bd5;
  color: #ffffff;
  font-size: 13px;
  font-weight: 600;
  flex-shrink: 0;
}

.user-info {
  flex: 1;
  text-align: left;
  min-width: 0;
}

.user-name {
  font-size: 14px;
  color: #0d0d0d;
  font-weight: 500;
  line-height: 1.2;
}

.user-plan {
  font-size: 12px;
  color: #8e8ea0;
  line-height: 1.4;
}

/* ================ Main ================ */
.main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  position: relative;
}

.topbar {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 56px;
  padding: 0 12px;
  flex-shrink: 0;
}

.topbar-title {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  border-radius: 8px;
  font-size: 18px;
  font-weight: 600;
  color: #0d0d0d;
  cursor: pointer;
  transition: background 0.15s;
}

.topbar-title:hover { background: rgba(0, 0, 0, 0.05); }

.topbar-title svg { color: #8e8ea0; }

.topbar-spacer { flex: 1; }

/* ================ Centered empty area ================ */
.centered-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 0 24px;
  gap: 32px;
}

.hero-title {
  font-size: 28px;
  font-weight: 500;
  color: #0d0d0d;
  text-align: center;
}

.centered-input {
  width: 100%;
  max-width: 768px;
}
</style>
