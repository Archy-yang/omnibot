<script setup lang="ts">
import { computed, ref } from 'vue';
import { marked } from 'marked';
import DOMPurify from 'dompurify';
import type { ChatMessageProps } from '@/types';
import { useToast } from '@/composables/useToast';

// 配置marked为同步解析
marked.use({
  async: false,
});

const props = withDefaults(defineProps<ChatMessageProps>(), {
  showTime: false,
});

const isUser = computed(() => props.message.role === 'user');

// 本会话流式产生的有序段落（text / tool 交错）。历史/刷新后的消息无此字段。
const segments = computed(() => props.message.segments ?? []);
const hasSegments = computed(() => segments.value.length > 0);

// 把任意 markdown 文本渲染成防 XSS 的 HTML。供 segments 里的每个 text 段
// 以及无 segments 时的 content 回退渲染共用。
function renderMarkdown(text: string): string {
  const html = marked.parse(text, {
    breaks: true, // 支持换行符转<br>
    gfm: true, // 支持GitHub Flavored Markdown
    async: false, // 强制同步解析
  }) as string;
  return DOMPurify.sanitize(html);
}

// 无 segments（历史消息 / 刷新后）时回退渲染整段 content
const renderedContent = computed(() => renderMarkdown(props.message.content));

// 切换某个 tool 段的展开状态（查看工具结果）
function toggleExpand(seg: { expanded?: boolean }) {
  seg.expanded = !seg.expanded;
}

const { success, error: toastError } = useToast();
const justCopied = ref(false);

// v2.0:助手消息反馈(纯前端 UI 演示,不入库)。
// up / down 状态在本组件内 toggle:再点同一个图标取消,点另一个互斥切换。
// 后端持久化留 backlog(v2.x+ 反馈表设计)。
const feedback = ref<'up' | 'down' | null>(null);
const handleFeedback = (next: 'up' | 'down') => {
  if (feedback.value === next) {
    feedback.value = null;
    return;
  }
  const isFirst = feedback.value === null;
  feedback.value = next;
  if (isFirst) {
    success(next === 'up' ? '已记录反馈,谢谢' : '已记录反馈,我们会改进');
  }
};

// 复制助手回复的原始 markdown 文本(不是渲染后的 HTML)。
// 用 navigator.clipboard 优先，浏览器不支持时降级到 textarea + execCommand。
async function handleCopy() {
  const text = props.message.content;
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
    } else {
      const textarea = document.createElement('textarea');
      textarea.value = text;
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      document.body.appendChild(textarea);
      textarea.select();
      const ok = document.execCommand('copy');
      document.body.removeChild(textarea);
      if (!ok) throw new Error('execCommand copy failed');
    }
    justCopied.value = true;
    success('已复制');
    setTimeout(() => {
      justCopied.value = false;
    }, 1500);
  } catch (err) {
    toastError('复制失败');
    console.error('Copy failed:', err);
  }
}

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

      <!-- Assistant: 左侧头像 + 名称头，内容缩进对齐头像右侧 -->
      <div v-else class="assistant-message">
        <div class="assistant-header">
          <div class="assistant-avatar" aria-hidden="true">O</div>
          <div class="assistant-name">OmniBot</div>
        </div>
        <div class="assistant-body">
          <!--
            v1.5.3：按 LLM 真实输出时序交错渲染段落。
            text 段渲 markdown，tool 段渲可点击展开的思考条，顺序即发生顺序。
            无 segments（历史 / 刷新后）时回退渲染整段 content。
          -->
          <template v-if="hasSegments">
            <template v-for="(seg, idx) in segments" :key="idx">
              <!-- 文本段 -->
              <div
                v-if="seg.type === 'text'"
                class="assistant-content markdown-body"
                v-html="renderMarkdown(seg.content)"
              ></div>

              <!-- 工具段：可点击展开看结果 -->
              <div v-else class="tool-segment">
                <button
                  type="button"
                  class="tool-segment-header"
                  :aria-expanded="seg.expanded ? 'true' : 'false'"
                  @click="toggleExpand(seg)"
                >
                  <!-- result 未回来时显示旋转齿轮,回来后显示静态齿轮(v2 设计) -->
                  <svg
                    v-if="seg.result === undefined"
                    class="tool-segment-spinner"
                    width="14" height="14" viewBox="0 0 24 24" fill="none"
                    stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
                  >
                    <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/>
                    <circle cx="12" cy="12" r="3"/>
                  </svg>
                  <svg
                    v-else
                    class="tool-segment-icon"
                    width="14" height="14" viewBox="0 0 24 24" fill="none"
                    stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
                  >
                    <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/>
                    <circle cx="12" cy="12" r="3"/>
                  </svg>
                  <span class="tool-segment-label">
                    {{ seg.result === undefined ? `正在调用 ${seg.label}…` : seg.label }}
                  </span>
                  <!-- result 回来后才显示展开图标:上下双箭头(v2 设计) -->
                  <svg
                    v-if="seg.result !== undefined"
                    class="tool-segment-chevron"
                    :class="{ 'is-open': seg.expanded }"
                    width="14" height="14" viewBox="0 0 24 24" fill="none"
                    stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
                  >
                    <path d="m7 15 5 5 5-5"/>
                    <path d="m7 9 5-5 5 5"/>
                  </svg>
                </button>
                <pre v-if="seg.expanded && seg.result !== undefined" class="tool-segment-result">{{ seg.result }}</pre>
              </div>
            </template>
          </template>

          <!-- 回退：无 segments 的历史消息 -->
          <div v-else class="assistant-content markdown-body" v-html="renderedContent"></div>

          <!-- 操作栏：仅在内容非空时显示，避免流式中途出现空按钮 -->
          <div v-if="message.content" class="assistant-actions">
            <button
              type="button"
              class="action-btn"
              :title="justCopied ? '已复制' : '复制'"
              :aria-label="justCopied ? '已复制' : '复制'"
              @click="handleCopy"
            >
              <!-- 已复制 → 对勾；未复制 → 双层方块图标 -->
              <svg v-if="justCopied" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="20 6 9 17 4 12"/>
              </svg>
              <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
              </svg>
            </button>

            <button
              type="button"
              class="action-btn"
              :class="{ 'is-active': feedback === 'up' }"
              :title="feedback === 'up' ? '已点赞' : '点赞'"
              :aria-label="feedback === 'up' ? '已点赞' : '点赞'"
              :aria-pressed="feedback === 'up' ? 'true' : 'false'"
              @click="handleFeedback('up')"
            >
              <!-- lucide thumbs-up -->
              <svg width="16" height="16" viewBox="0 0 24 24" :fill="feedback === 'up' ? 'currentColor' : 'none'" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M7 10v12"/>
                <path d="M15 5.88 14 10h5.83a2 2 0 0 1 1.92 2.56l-2.33 8A2 2 0 0 1 17.5 22H7a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2h2.76a2 2 0 0 0 1.79-1.11L15 .5"/>
              </svg>
            </button>

            <button
              type="button"
              class="action-btn"
              :class="{ 'is-active': feedback === 'down' }"
              :title="feedback === 'down' ? '已踩' : '踩'"
              :aria-label="feedback === 'down' ? '已踩' : '踩'"
              :aria-pressed="feedback === 'down' ? 'true' : 'false'"
              @click="handleFeedback('down')"
            >
              <!-- lucide thumbs-down -->
              <svg width="16" height="16" viewBox="0 0 24 24" :fill="feedback === 'down' ? 'currentColor' : 'none'" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M17 14V2"/>
                <path d="M9 18.12 10 14H4.17a2 2 0 0 1-1.92-2.56l2.33-8A2 2 0 0 1 6.5 2H17a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-2.76a2 2 0 0 0-1.79 1.11L9 23.5"/>
              </svg>
            </button>
          </div>
        </div>
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

/* 头部：头像 + 名称，标识「OmniBot 在回复你」 */
.assistant-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.assistant-avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: linear-gradient(135deg, #2080f0 0%, #4dabff 100%);
  color: #ffffff;
  font-size: 15px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  user-select: none;
  letter-spacing: -0.5px;
}

.assistant-name {
  font-size: 13px;
  color: #6b7280;
  font-weight: 500;
}

/* 正文：缩进对齐头像右侧（32px 头像 + 10px 间距 = 42px） */
.assistant-body {
  padding-left: 42px;
}

.assistant-content {
  color: #0d0d0d;
  font-size: 15px;
  line-height: 1.75;
  white-space: pre-wrap;
  word-break: break-word;
}

/* 操作栏：复制等按钮，hover 时整条消息行才会浮现，平时保持低存在感 */
.assistant-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 8px;
  opacity: 0;
  transition: opacity 0.2s ease;
}

.assistant-message:hover .assistant-actions,
.assistant-actions:focus-within {
  opacity: 1;
}

.action-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border: none;
  background: transparent;
  border-radius: 6px;
  cursor: pointer;
  color: #6b7280;
  transition: all 0.15s ease;
  padding: 0;
}

.action-btn:hover {
  background: rgba(0, 0, 0, 0.05);
  color: #111827;
}

.action-btn:active {
  background: rgba(0, 0, 0, 0.08);
}

/* v2.0:thumbs up/down 选中态——填充 + 主题蓝,再次点击取消 */
.action-btn.is-active {
  color: #2563eb;
}

.action-btn.is-active:hover {
  background: rgba(37, 99, 235, 0.08);
  color: #1d4ed8;
}

/* ===== 工具思考段 (v1.5.3) =====
   按时序交错出现在文本之间，可点击展开看工具结果。
   ChatGPT / Claude 风格的低对比度「思考」条带，不打扰阅读。 */
.tool-segment {
  margin: 8px 0;
}

.tool-segment-header {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px 12px;
  background: #f9fafb;
  border: none;
  border-left: 2px solid #e5e7eb;
  border-radius: 8px;
  font-size: 13px;
  color: #6b7280;
  line-height: 1.5;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease;
}

.tool-segment-header:hover {
  background: #f3f4f6;
}

.tool-segment-icon,
.tool-segment-spinner {
  flex-shrink: 0;
  color: #9ca3af;
}

.tool-segment-spinner {
  animation: tool-spin 0.8s linear infinite;
}

@keyframes tool-spin {
  to {
    transform: rotate(360deg);
  }
}

.tool-segment-label {
  flex: 1;
}

.tool-segment-chevron {
  flex-shrink: 0;
  color: #9ca3af;
  transition: color 0.15s ease;
}

/* v2.0:展开图标是 chevrons-up-down(上下双 V),旋转 180° 视觉等价,
   不旋转,改为加深颜色表示展开态 */
.tool-segment-chevron.is-open {
  color: #6b7280;
}

/* 展开区：限高滚动，长结果（RSS 全文 / 长 JSON）内部滚动，不撑乱对话 */
.tool-segment-result {
  margin: 4px 0 0 0;
  padding: 10px 12px;
  max-height: 240px;
  overflow-y: auto;
  background: #f4f4f4;
  border-radius: 8px;
  font-family: 'Monaco', 'Menlo', 'Courier New', monospace;
  font-size: 12px;
  line-height: 1.6;
  color: #374151;
  white-space: pre-wrap;
  word-break: break-word;
}

/* ===== Markdown样式 ===== */
:deep(.markdown-body) h1,
:deep(.markdown-body) h2,
:deep(.markdown-body) h3,
:deep(.markdown-body) h4,
:deep(.markdown-body) h5,
:deep(.markdown-body) h6 {
  margin: 20px 0 10px 0;
  font-weight: 600;
  line-height: 1.5;
}

:deep(.markdown-body) h1 { font-size: 24px; border-bottom: 1px solid #e5e7eb; padding-bottom: 6px; }
:deep(.markdown-body) h2 { font-size: 20px; border-bottom: 1px solid #e5e7eb; padding-bottom: 4px; }
:deep(.markdown-body) h3 { font-size: 18px; }
:deep(.markdown-body) h4 { font-size: 16px; }

:deep(.markdown-body) p {
  margin: 0 0 2px 0;
}

/* 彻底重置所有列表样式，强制覆盖任何默认样式 */
:deep(.markdown-body) {
  white-space: normal !important; /* 覆盖父元素的pre-wrap，消除换行导致的空隙 */
}

:deep(.markdown-body) ul,
:deep(.markdown-body) ol {
  margin: -2px 0 0 0 !important;
  padding: 0 0 0 36px !important; /* 整体缩进加大，给列表符号留足够空间 */
}

/* 无序列表自定义圆点 */
:deep(.markdown-body) ul {
  list-style: none !important;
}

/* 所有级别的ul li，不管嵌套多少层，都强制有足够缩进，绝对不会盖住圆点 */
:deep(.markdown-body) ul li,
:deep(.markdown-body) ul ul li,
:deep(.markdown-body) ul ul ul li,
:deep(.markdown-body) ol ul li,
:deep(.markdown-body) ol ul ul li {
  position: relative !important;
  margin: 0 0 6px 0 !important;
  padding: 0 0 0 28px !important; /* 强制28px缩进，给圆点留足够空间，任何层级都不会重叠 */
  line-height: 1.5 !important;
  list-style: none !important;
}

/* 有序列表用原生编号，保证100%生效，和ul视觉对齐 */
:deep(.markdown-body) ol {
  list-style: decimal !important; /* 原生数字编号，肯定显示 */
  padding-left: 32px !important; /* 和ul的24px + 8px空隙对齐，视觉一致 */
  margin: -2px 0 0 0 !important;
}

:deep(.markdown-body) ol li {
  margin: 0 0 6px 0 !important;
  padding-left: 4px !important; /* 调整内容和编号之间的空隙 */
  line-height: 1.5 !important;
}

/* 多级嵌套列表强制缩进，层级清晰，100%避免重叠 */
:deep(.markdown-body) li > ul,
:deep(.markdown-body) li > ol {
  padding-left: 32px !important; /* 每一级嵌套额外强制缩进32px，绝对不会重叠 */
  margin: 6px 0 0 0 !important; /* 子列表和父项之间留舒适间距 */
}

/* 子列表里的ul圆点位置强制修正，绝对不和文字重叠 */
:deep(.markdown-body) li > ul li::before {
  left: 4px !important;
  top: 0 !important;
  position: absolute !important;
}

/* 多级嵌套列表样式，层级清晰 */
:deep(.markdown-body) ul ul li::before {
  content: "◦"; /* 二级无序列表：空心圆点 */
  font-size: 18px;
  position: absolute;
  left: 4px;
  top: 0;
  line-height: 1.5 !important;
}

:deep(.markdown-body) ul ul ul li::before {
  content: "▪"; /* 三级无序列表：实心方块 */
  font-size: 14px;
  position: absolute;
  left: 4px;
  top: 0;
  line-height: 1.5 !important;
}

/* 任务列表（Todo List）样式 */
:deep(.markdown-body) .task-list-item {
  list-style: none !important;
  position: relative;
  padding-left: 28px !important; /* 给勾选框留位置 */
}

:deep(.markdown-body) .task-list-item-checkbox {
  position: absolute;
  left: 0;
  top: 2px;
  width: 16px;
  height: 16px;
  accent-color: #2080f0; /* 勾选框用主题蓝色 */
  cursor: default;
}

/* 已完成的任务加删除线、变灰 */
:deep(.markdown-body) .task-list-item:has(input:checked) {
  color: #6b7280;
}
:deep(.markdown-body) .task-list-item input:checked + * {
  text-decoration: line-through;
}

/* 无序列表圆点，精确对齐 */
:deep(.markdown-body) ul li::before {
  content: "•" !important;
  position: absolute !important;
  left: 4px !important; /* 圆点和内容之间留4px空隙 */
  top: 0 !important;
  color: #333 !important;
  font-size: 16px !important;
  line-height: 1.5 !important;
  z-index: 999 !important; /* 圆点层级最高，永远不会被文字盖住 */
}

/* 有序列表样式 */
:deep(.markdown-body) ol {
  list-style: decimal !important;
}

:deep(.markdown-body) ol li {
  padding-left: 4px !important;
}

/* 清除列表项内所有元素的边距 */
:deep(.markdown-body) ul li *,
:deep(.markdown-body) ol li * {
  margin: 0 !important;
  padding: 0 !important;
  line-height: inherit !important;
}

:deep(.markdown-body) blockquote {
  margin: 12px 0;
  padding: 8px 16px;
  border-left: 4px solid #e5e7eb;
  color: #6b7280;
  background: #f9fafb;
  border-radius: 0 4px 4px 0;
}

:deep(.markdown-body) code {
  background: #f4f4f4;
  padding: 2px 6px;
  border-radius: 4px;
  font-family: 'Monaco', 'Menlo', 'Courier New', monospace;
  font-size: 0.9em;
}

:deep(.markdown-body) pre {
  margin: 12px 0;
  padding: 16px;
  background: #f4f4f4;
  border-radius: 8px;
  overflow-x: auto;
  font-family: 'Monaco', 'Menlo', 'Courier New', monospace;
  font-size: 0.9em;
  line-height: 1.6;
}

:deep(.markdown-body) pre code {
  padding: 0;
  background: transparent;
}

:deep(.markdown-body) a {
  color: #2563eb;
  text-decoration: none;
  border-bottom: 1px solid transparent;
  transition: all 0.2s;
}

:deep(.markdown-body) a:hover {
  border-bottom-color: #2563eb;
}

:deep(.markdown-body) table {
  width: 100%;
  margin: 12px 0;
  border-collapse: collapse;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
}

:deep(.markdown-body) th,
:deep(.markdown-body) td {
  padding: 8px 12px;
  border: 1px solid #e5e7eb;
}

:deep(.markdown-body) th {
  background: #f9fafb;
  font-weight: 600;
}

:deep(.markdown-body) hr {
  margin: 20px 0;
  border: none;
  border-top: 1px solid #e5e7eb;
}
</style>
