<template>
  <div class="chat-input-wrap">
    <div class="chat-input-inner">
      <div class="chat-input-box">
        <!-- 输入区域（在上） -->
        <textarea
          ref="textareaRef"
          v-model="inputText"
          placeholder="有问题，尽管问"
          :disabled="false"
          rows="1"
          class="chat-textarea"
          @input="handleInput"
          @keydown="handleKeydown"
          @compositionstart="isComposing = true"
          @compositionend="isComposing = false"
        />

        <!-- 底部工具栏（在下） -->
        <div class="bottom-toolbar">
          <div class="left-tools">
            <!-- 模式选择菜单 -->
            <NPopover
              v-model:show="showMenu"
              placement="top-start"
              trigger="click"
              :show-arrow="false"
            >
              <template #trigger>
                <button class="plus-btn" :class="{ active: showMenu }">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <line x1="12" y1="5" x2="12" y2="19"/>
                    <line x1="5" y1="12" x2="19" y2="12"/>
                  </svg>
                </button>
              </template>
              <div class="mode-menu">
                <div class="mode-item" @click="toggleThinking">
                  <div class="mode-icon">
                    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <path d="M9.5 19h5a.5.5 0 0 1 0 1h-5a.5.5 0 0 1 0-1z"/>
                      <path d="M12 3a6 6 0 0 1 6 6c0 2.5-1.5 4.5-3 6l-3 3-3-3c-1.5-1.5-3-3.5-3-6a6 6 0 0 1 6-6z"/>
                    </svg>
                  </div>
                  <div class="mode-info">
                    <div class="mode-name">{{ isThinking ? '关闭思考模式' : '开启思考模式' }}</div>
                    <div class="mode-desc">开启后支持多步推理和工具调用</div>
                  </div>
                </div>
              </div>
            </NPopover>

            <!-- 思考模式标签，开启时显示 -->
            <div
              v-if="isThinking"
              class="thinking-tag"
              @mouseenter="showClose = true"
              @mouseleave="showClose = false"
            >
              <button v-if="showClose" class="close-btn" @click.stop="toggleThinking">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <line x1="18" y1="6" x2="6" y2="18" />
                  <line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
              <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#2080f0" stroke-width="2">
                <circle cx="12" cy="12" r="10" />
                <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" />
                <line x1="12" y1="17" x2="12.01" y2="17" />
              </svg>
              <span>思考</span>
            </div>
          </div>

          <div class="right-tools">
            <button
              class="tool-btn mic-btn"
              title="语音输入"
              @click="handleVoiceInput"
            >
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/>
                <path d="M19 10v2a7 7 0 0 1-14 0v-2"/>
                <line x1="12" y1="19" x2="12" y2="23"/>
                <line x1="8" y1="23" x2="16" y2="23"/>
              </svg>
            </button>

            <button
              class="send-btn"
              :class="{ active: canSend }"
              :disabled="!canSend"
              @click="handleSend"
            >
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                <line x1="12" y1="19" x2="12" y2="5"/>
                <polyline points="5 12 12 5 19 12"/>
              </svg>
            </button>
          </div>
        </div>
      </div>

      <p class="chat-input-hint">AI可能会犯错，重要信息请核实</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, nextTick } from 'vue'
import { NPopover } from 'naive-ui'
import { useChatStore } from '@/stores/chat'
import { useToast } from '@/composables/useToast'

const chatStore = useChatStore()
const { error, info } = useToast()

const inputText = ref('')
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const isComposing = ref(false)
const isThinking = ref(false)
const showMenu = ref(false)
const showClose = ref(false)

const canSend = computed(() => {
  return inputText.value.trim().length > 0
})

// 自动调整高度
function autoResize() {
  if (!textareaRef.value) return
  const textarea = textareaRef.value
  textarea.style.height = 'auto'
  textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px'
}

function handleInput() {
  nextTick(() => {
    autoResize()
  })
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && !event.shiftKey && !isComposing.value) {
    event.preventDefault()
    handleSend()
  }
}

function handleSend() {
  if (!canSend.value) return

  const text = inputText.value.trim()
  inputText.value = ''
  nextTick(autoResize)

  chatStore.sendMessage(text, isThinking.value).catch((err: Error) => {
    error(err.message || '发送失败')
  })
}

function handleAttach() {
  // 预留：打开附件选择
}

function handleVoiceInput() {
  info('语音输入功能即将上线')
}

function toggleThinking() {
  isThinking.value = !isThinking.value
}

// 从 localStorage 恢复思考模式状态
onMounted(() => {
  const saved = localStorage.getItem('chat-thinking-mode')
  if (saved === 'true') {
    isThinking.value = true
  }
  autoResize()
})

// 持久化思考模式
watch(isThinking, (val) => {
  localStorage.setItem('chat-thinking-mode', String(val))
})
</script>

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

/* 统一上下布局 */
.chat-input-box {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: stretch;
  width: 100%;
  background: #ffffff;
  border: 1px solid #e5e7eb;
  border-radius: 16px;
  transition: all 0.2s;
  padding: 12px;
}

.chat-input-box:focus-within {
  border-color: #2080f0;
  box-shadow: 0 0 0 2px rgba(32, 128, 240, 0.1);
}

/* 输入区域 */
.chat-textarea {
  width: 100%;
  resize: none;
  background: transparent;
  outline: none;
  border: none;
  padding: 0 0 12px 0;
  font-size: 15px;
  line-height: 1.5;
  color: #333;
  font-family: inherit;
  max-height: 200px;
  overflow-y: auto;
  min-height: 24px;
  text-align: left;
}

.chat-textarea::placeholder {
  color: #9ca3af;
}

/* 底部工具栏 */
.bottom-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  min-height: 36px;
}

.left-tools {
  display: flex;
  align-items: center;
  gap: 8px;
}

.right-tools {
  display: flex;
  align-items: center;
  gap: 6px;
}

/* 按钮通用样式 */
.plus-btn, .tool-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  border-radius: 8px;
  cursor: pointer;
  color: #6b7280;
  flex-shrink: 0;
  transition: all 0.15s;
}

.plus-btn:hover, .tool-btn:hover {
  background: rgba(0, 0, 0, 0.05);
  color: #374151;
}

/* 思考模式标签 */
.thinking-tag {
  display: flex;
  align-items: center;
  gap: 4px;
  color: #2080f0;
  font-size: 13px;
  font-weight: 500;
  user-select: none;
  background: rgba(32, 128, 240, 0.1);
  padding: 2px 6px;
  border-radius: 6px;
  transition: all 0.2s;
}

/* 关闭按钮 */
.close-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  border: none;
  background: transparent;
  color: #2080f0;
  cursor: pointer;
  padding: 0;
  border-radius: 3px;
  transition: all 0.15s;
}

.close-btn:hover {
  background: rgba(32, 128, 240, 0.2);
}

/* 思考模式按钮激活态 */
.thinking-btn.active {
  color: #2080f0;
  background: rgba(32, 128, 240, 0.1);
}

/* 发送按钮 */
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
  flex-shrink: 0;
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
  color: #9ca3af;
  margin-top: 12px;
}

/* 模式菜单样式 */
.mode-menu {
  width: 280px;
  padding: 8px 0;
}

.mode-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  cursor: pointer;
  border-radius: 8px;
  transition: background 0.15s;
}

.mode-item:hover {
  background: rgba(0, 0, 0, 0.04);
}

.mode-icon {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #6b7280;
  flex-shrink: 0;
}

.mode-info {
  flex: 1;
  min-width: 0;
}

.mode-name {
  font-size: 14px;
  font-weight: 500;
  color: #111827;
  line-height: 1.4;
}

.mode-desc {
  font-size: 12px;
  color: #6b7280;
  line-height: 1.4;
  margin-top: 2px;
}
</style>
