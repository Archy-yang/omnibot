<script setup lang="ts">
import { ref, watch, nextTick, onMounted } from 'vue';
import type { ChatInputProps, ChatInputEmits } from '@/types';

const props = withDefaults(defineProps<ChatInputProps>(), {
  placeholder: '有问题，尽管问',
  disabled: false,
  isLoading: false,
});

const emit = defineEmits<ChatInputEmits>();

const textareaRef = ref<HTMLTextAreaElement | null>(null);
const inputValue = ref(props.modelValue);
const isComposing = ref(false);

const autoResize = () => {
  const el = textareaRef.value;
  if (!el) return;
  el.style.height = 'auto';
  const next = Math.min(el.scrollHeight, 200);
  el.style.height = `${next}px`;
};

watch(() => props.modelValue, (v) => {
  inputValue.value = v;
  nextTick(autoResize);
});

watch(inputValue, (v) => {
  emit('update:modelValue', v);
  autoResize();
});

const handleKeydown = (e: KeyboardEvent) => {
  if (e.key === 'Enter' && !e.shiftKey && !isComposing.value) {
    e.preventDefault();
    handleSend();
  }
};

const handleSend = () => {
  const content = inputValue.value.trim();
  if (!content || props.disabled || props.isLoading) return;
  emit('send', content);
  inputValue.value = '';
  nextTick(autoResize);
};

onMounted(autoResize);
</script>

<template>
  <div class="chat-input-wrap">
    <div class="chat-input-inner">
      <div class="chat-input-box">
        <button class="plus-btn">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="5" x2="12" y2="19"/>
            <line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
        </button>

        <textarea
          ref="textareaRef"
          v-model="inputValue"
          :placeholder="placeholder"
          :disabled="disabled || isLoading"
          rows="1"
          class="chat-textarea"
          @keydown="handleKeydown"
          @compositionstart="isComposing = true"
          @compositionend="isComposing = false"
        />

        <div class="right-actions">
          <button class="icon-btn mic-btn" title="语音输入">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/>
              <path d="M19 10v2a7 7 0 0 1-14 0v-2"/>
              <line x1="12" y1="19" x2="12" y2="23"/>
              <line x1="8" y1="23" x2="16" y2="23"/>
            </svg>
          </button>

          <button
            class="send-btn"
            :class="{ active: inputValue.trim() && !disabled && !isLoading }"
            :disabled="disabled || isLoading || !inputValue.trim()"
            @click="handleSend"
          >
            <svg v-if="!isLoading" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <line x1="12" y1="19" x2="12" y2="5"/>
              <polyline points="5 12 12 5 19 12"/>
            </svg>
            <div v-else class="spinner"/>
          </button>
        </div>
      </div>

      <p class="chat-input-hint">OmniBot 可能会犯错，重要信息请核实</p>
    </div>
  </div>
</template>

<style scoped>
.chat-input-wrap {
  width: 100%;
  background: #ffffff;
  padding: 12px 24px 16px;
  flex-shrink: 0;
}

.chat-input-inner {
  width: 100%;
  max-width: 768px;
  margin: 0 auto;
}

.chat-input-box {
  position: relative;
  display: flex;
  align-items: flex-end;
  width: 100%;
  background: #f4f4f4;
  border-radius: 26px;
  transition: all 0.2s;
}

.chat-input-box:focus-within {
  box-shadow: 0 2px 16px rgba(0, 0, 0, 0.08);
}

.plus-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  margin-left: 8px;
  margin-bottom: 4px;
  border: none;
  background: transparent;
  border-radius: 50%;
  cursor: pointer;
  color: #5d5d6e;
  flex-shrink: 0;
  transition: background 0.15s;
}

.plus-btn:hover { background: rgba(0, 0, 0, 0.05); }

.chat-textarea {
  flex: 1;
  resize: none;
  background: transparent;
  outline: none;
  border: none;
  padding: 14px 12px;
  font-size: 15px;
  line-height: 1.5;
  color: #0d0d0d;
  font-family: inherit;
  max-height: 200px;
  overflow-y: auto;
}

.chat-textarea::placeholder {
  color: #8e8ea0;
}

.chat-textarea:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.right-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  padding-right: 8px;
  padding-bottom: 4px;
  flex-shrink: 0;
}

.mic-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: none;
  background: transparent;
  border-radius: 50%;
  cursor: pointer;
  color: #5d5d6e;
  transition: background 0.15s;
}

.mic-btn:hover { background: rgba(0, 0, 0, 0.05); }

.send-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: none;
  border-radius: 50%;
  background: #e5e5e5;
  color: #8e8ea0;
  cursor: not-allowed;
  transition: all 0.15s;
}

.send-btn.active {
  background: #000000;
  color: #ffffff;
  cursor: pointer;
}

.send-btn.active:hover { background: #2a2a2a; }

.chat-input-hint {
  text-align: center;
  font-size: 11px;
  color: #8e8ea0;
  margin-top: 12px;
}

.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid #ffffff;
  border-top-color: transparent;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}
</style>
