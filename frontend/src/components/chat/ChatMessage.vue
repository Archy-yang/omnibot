<script setup lang="ts">
import { computed } from 'vue';
import type { ChatMessageProps } from '@/types';

const props = withDefaults(defineProps<ChatMessageProps>(), {
  showTime: false,
});

const isUser = computed(() => props.message.role === 'user');

defineEmits<{
  copy: [content: string];
  resend: [message: typeof props.message];
}>();
</script>

<template>
  <div class="msg-row">
    <div class="msg-wrap">
      <!-- User: right-aligned gray bubble -->
      <div v-if="isUser" class="user-message">
        <div class="user-bubble">{{ message.content }}</div>
      </div>

      <!-- Assistant: no avatar, no background, full width -->
      <div v-else class="assistant-message">
        <div class="assistant-content">{{ message.content }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.msg-row {
  width: 100%;
  padding: 12px 24px;
}

.msg-wrap {
  max-width: 768px;
  margin: 0 auto;
}

/* ===== User Message ===== */
.user-message {
  display: flex;
  justify-content: flex-end;
}

.user-bubble {
  max-width: 75%;
  background: #f4f4f4;
  color: #0d0d0d;
  padding: 10px 18px;
  border-radius: 22px;
  font-size: 15px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  text-align: left;
}

/* ===== Assistant Message ===== */
.assistant-message {
  width: 100%;
}

.assistant-content {
  color: #0d0d0d;
  font-size: 15px;
  line-height: 1.75;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
