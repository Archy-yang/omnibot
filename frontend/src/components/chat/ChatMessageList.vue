<script setup lang="ts">
import { ref, watch, nextTick, onMounted } from 'vue';
import type { ChatMessageListProps } from '@/types';
import ChatMessage from './ChatMessage.vue';

const props = withDefaults(defineProps<ChatMessageListProps>(), {
  isLoading: false,
  hasMoreHistory: false,
  isLoadingOlder: false,
});

// 冒泡消息内部事件(任务卡片 → 页面打开任务中心)+ 历史向前分页
const emit = defineEmits<{
  'open-task': [taskId: number];
  'load-older': [];
}>();

const containerRef = ref<HTMLElement | null>(null);
const shouldAutoScroll = ref(true);
// 向前分页:前插更早消息后按 scrollHeight 差值回补视口,避免列表跳动
const pendingRestore = ref<number | null>(null);

const scrollToBottom = async () => {
  if (!shouldAutoScroll.value) return;
  await nextTick();
  if (containerRef.value) {
    containerRef.value.scrollTop = containerRef.value.scrollHeight;
  }
};

const handleScroll = () => {
  if (!containerRef.value) return;
  const { scrollTop, scrollHeight, clientHeight } = containerRef.value;
  shouldAutoScroll.value = scrollHeight - scrollTop - clientHeight < 50;
  // 滚近顶部时加载更早的历史(持续对话不一次性全载)
  if (scrollTop < 60 && props.hasMoreHistory && !props.isLoadingOlder) {
    pendingRestore.value = scrollHeight;
    emit('load-older');
  }
};

watch(
  () => props.messages.length,
  async (newLen, oldLen) => {
    if (pendingRestore.value !== null && newLen > oldLen) {
      // 前插更早消息:保持视口停在原来看到的位置
      const prevHeight = pendingRestore.value;
      pendingRestore.value = null;
      await nextTick();
      if (containerRef.value) {
        containerRef.value.scrollTop = containerRef.value.scrollHeight - prevHeight;
      }
      return;
    }
    scrollToBottom();
  }
);
onMounted(scrollToBottom);

const examplePrompts = [
  { title: '解释一个概念', subtitle: '用通俗的语言解释「量子纠缠」' },
  { title: '写一段代码', subtitle: '用 TypeScript 实现一个防抖函数' },
  { title: '帮我润色文案', subtitle: '帮我把这段产品介绍写得更专业' },
  { title: '翻译并讲解', subtitle: '翻译这段英文并解释难点词汇' },
];
</script>

<template>
  <div
    ref="containerRef"
    class="flex-1 overflow-y-auto chat-scroll"
    @scroll="handleScroll"
  >
    <!-- Empty State -->
    <div
      v-if="messages.length === 0 && !isLoading"
      class="h-full flex flex-col items-center justify-center px-4 py-12"
    >
      <!-- Logo -->
      <div class="w-16 h-16 mb-6 rounded-full bg-[var(--accent)] flex items-center justify-center">
        <svg viewBox="0 0 41 41" fill="white" width="36" height="36">
          <path d="M37.5324 16.8707c.9886-2.9594.6594-6.2024-.9027-8.8945-2.348-4.0728-7.0494-6.1685-11.62-5.1791C22.6324 1.0184 19.7864.0035 16.9043.0035c-4.7113 0-8.8888 3.0182-10.3375 7.4773C3.4754 8.4661 1.0148 10.7402.0093 13.7066c-2.354 4.0712-2.1146 9.2009.5901 12.9772-.9886 2.9595-.6594 6.2024.9027 8.8946 2.348 4.0727 7.0494 6.1684 11.62 5.179 2.3792 2.5777 5.226 3.5926 8.1081 3.5926 4.7113 0 8.8888-3.0181 10.3375-7.4773 2.9508-.9852 5.4114-3.2594 6.4169-6.2258 2.3539-4.0712 2.1145-9.2009-.5902-12.9772zM22.4068 38.6485a7.704 7.704 0 01-4.9486-1.7912l.2442-.1382 8.2147-4.7457a1.3464 1.3464 0 00.6772-1.1689v-11.5853l3.4729 2.0061a.1236.1236 0 01.0673.0962v9.5933a7.7397 7.7397 0 01-7.7277 7.7337z"/>
        </svg>
      </div>
      <h1 class="empty-title">OmniBot</h1>
      <p class="empty-subtitle">有什么可以帮你的吗？</p>

      <!-- Example prompts -->
      <div class="prompts-grid">
        <button
          v-for="(p, i) in examplePrompts"
          :key="i"
          class="prompt-card"
        >
          <div class="prompt-title">{{ p.title }}</div>
          <div class="prompt-subtitle">{{ p.subtitle }}</div>
        </button>
      </div>
    </div>

    <!-- Loading state (initial) -->
    <div
      v-else-if="isLoading && messages.length === 0"
      class="h-full flex items-center justify-center"
    >
      <div class="w-6 h-6 border-2 border-[var(--border-l2)] border-t-[var(--accent)] rounded-full animate-spin"/>
    </div>

    <!-- Messages -->
    <div v-else class="messages-area">
      <!-- 历史向前分页指示:顶部加载中 / 已全部加载 -->
      <div v-if="isLoadingOlder" class="history-status">
        <span class="history-status-spinner"></span>加载更早的消息...
      </div>
      <div v-else-if="!hasMoreHistory && messages.length > 0" class="history-status history-status-end">
        以上是全部历史
      </div>

      <ChatMessage
        v-for="message in messages"
        :key="message.id"
        :message="message"
        @open-task="(tid: number) => emit('open-task', tid)"
      />

      <!-- Typing indicator -->
      <div v-if="isLoading" class="typing-row">
        <div class="typing-wrap">
          <div class="typing-dots">
            <span class="dot"></span>
            <span class="dot"></span>
            <span class="dot"></span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.chat-scroll {
  background: var(--bg-base);
}

.empty-title {
  font-size: 24px;
  font-weight: 600;
  color: var(--label-primary);
  margin-bottom: 8px;
}

.empty-subtitle {
  font-size: 16px;
  color: var(--text-secondary);
  margin-bottom: 54px;
}

.messages-area {
  padding: 16px 0 24px;
}

/* 历史向前分页:顶部状态条 */
.history-status {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 8px 0 16px;
  font-size: 12px;
  color: var(--label-tertiary);
}

.history-status-spinner {
  width: 12px;
  height: 12px;
  border: 1.5px solid var(--border-l2);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: history-spin 0.8s linear infinite;
}

@keyframes history-spin {
  to { transform: rotate(360deg); }
}

.typing-row {
  width: 100%;
  padding: 12px 24px;
}

.typing-wrap {
  max-width: 768px;
  margin: 0 auto;
}

.typing-dots {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 0;
}

.prompts-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  width: 100%;
  max-width: 640px;
  padding: 0 16px;
}

@media (max-width: 640px) {
  .prompts-grid {
    grid-template-columns: 1fr;
    max-width: 420px;
  }
}

.prompt-card {
  text-align: left;
  padding: 16px 20px;
  background: var(--bg-base);
  border: 0.5px solid var(--border-l2);
  border-radius: 12px;
  cursor: pointer;
  transition: all 0.2s;
}

.prompt-card:hover {
  background: var(--bg-hover);
  border-color: var(--border-l3);
}

.prompt-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--label-primary);
  margin-bottom: 6px;
  line-height: 1.4;
}

.prompt-subtitle {
  font-size: 12px;
  color: var(--label-tertiary);
  line-height: 1.5;
}

.dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  background: var(--label-caption);
  border-radius: 50%;
  animation: dot 1.4s infinite ease-in-out;
}
.dot:nth-child(2) { animation-delay: .2s; }
.dot:nth-child(3) { animation-delay: .4s; }

@keyframes dot {
  0%, 60%, 100% { opacity: .3; transform: translateY(0); }
  30% { opacity: 1; transform: translateY(-3px); }
}
</style>
