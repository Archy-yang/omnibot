<template>
  <div class="chat-input-wrap">
    <div class="chat-input-inner">
      <div class="chat-input-box">
        <!-- 输入区域（在上） -->
        <textarea
          ref="textareaRef"
          v-model="inputText"
          :placeholder="props.placeholder"
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
            <!--
              v1.5.2 起取消「思考模式」开关。所有对话默认走 Agent 流式路径，
              是否调用工具由 LLM 自动判断。底部左侧暂无功能，先留空，
              将来要加附件/语音切换等再补。
            -->
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
import { ref, computed, onMounted, nextTick } from 'vue'
import { useChatStore } from '@/stores/chat'
import { useToast } from '@/composables/useToast'

const props = withDefaults(defineProps<{
  /** 输入框占位符。v2.0 起空状态/有消息时由父组件切换文案。 */
  placeholder?: string
}>(), {
  placeholder: '有问题，尽管问',
})

const chatStore = useChatStore()
const { error, info } = useToast()

const inputText = ref('')
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const isComposing = ref(false)

const canSend = computed(() => {
  return inputText.value.trim().length > 0
})

// 自动调整高度。
// 滚动条策略:低于最大高度一律 overflow-y: hidden——此时高度=内容高度,字不会被裁;
// 仅当内容触顶(max)才开 auto 滚动。此前恒为 auto,行高取整让单行时 scrollHeight
// 比 clientHeight 多 1px,右侧常驻一小截滚动条(2026-09-25 反馈)。
const MAX_TEXTAREA_HEIGHT = 200

function autoResize() {
  if (!textareaRef.value) return
  const textarea = textareaRef.value
  // 测量时先藏滚动条,避免滚动条宽度挤占内容区导致 scrollHeight 虚高
  textarea.style.overflowY = 'hidden'
  textarea.style.height = 'auto'
  const content = textarea.scrollHeight
  textarea.style.height = Math.min(content, MAX_TEXTAREA_HEIGHT) + 'px'
  if (content > MAX_TEXTAREA_HEIGHT) {
    textarea.style.overflowY = 'auto'
  }
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

  chatStore.sendMessage(text).catch((err: Error) => {
    error(err.message || '发送失败')
  })
}

function handleVoiceInput() {
  info('语音输入功能即将上线')
}

onMounted(() => {
  // v1.5.2 起不再读取 chat-thinking-mode localStorage（已废弃）。
  // 旧的本地残留值不会影响新行为，浏览器 localStorage 自然过期或用户手动清。
  autoResize()
})
</script>

<style scoped>
.chat-input-wrap {
  width: 100%;
  background: var(--bg-base);
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
  background: var(--bg-base);
  border: 0.5px solid var(--border-l4);
  border-radius: 16px;
  transition: all 0.2s;
  padding: 12px;
}

.chat-input-box:focus-within {
  border-color: var(--accent);
  box-shadow: 0 0 0 2px rgba(65, 118, 230, 0.08);
}

/* 输入区域 */
.chat-textarea {
  width: 100%;
  resize: none;
  background: transparent;
  outline: none;
  border: none;
  padding: 0 0 12px 0;
  font-size: 14px;
  line-height: 1.6;
  color: var(--label-primary);
  font-family: inherit;
  max-height: 200px;
  overflow-y: auto;
  min-height: 24px;
  text-align: left;
}

.chat-textarea::placeholder {
  color: var(--label-caption);
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
  /* 占位高度，避免左右对齐时左侧塌陷 */
  min-height: 32px;
}

.right-tools {
  display: flex;
  align-items: center;
  gap: 6px;
}

/* 按钮通用样式 */
.tool-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  border-radius: 8px;
  cursor: pointer;
  color: var(--label-tertiary);
  flex-shrink: 0;
  transition: all 0.15s;
}

.tool-btn:hover {
  background: var(--bg-hover);
  color: var(--label-secondary);
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
  background: var(--accent-dimmed);
  color: var(--label-caption);
  cursor: not-allowed;
  transition: all 0.15s;
  flex-shrink: 0;
}

.send-btn.active {
  background: var(--btn-primary-fill);
  color: var(--btn-primary-foreground);
  cursor: pointer;
}

.send-btn.active:hover { background: var(--btn-primary-hover); }

.chat-input-hint {
  text-align: center;
  font-size: 11px;
  color: var(--label-caption);
  margin-top: 12px;
}
</style>
