<script setup lang="ts">
import { computed, ref, watch } from 'vue';
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

// ===== 思考模式 C5:思考过程 vs 最终回复分拆(基于 segment role) =====
// 后端在回复轮发 AgentEventFinal,前端 onFinal 把对应 text 段标 role=final。
// 思考块 = 所有 role!=='final' 的段(thought text + 全部 tool);
// 主气泡 = role==='final' 的 text 段。
// 老消息(无 role 字段):回退--最后一个 text 段当 final,其余 thought。

// 最终回复文本:取 role==='final' 的 text 段;无则回退 message.content。
// 兼容老数据:若所有 text 段都无 role,取最后一个 text 段作 final。
const finalText = computed(() => {
  const segs = segments.value;
  // 优先 role=final 的段
  for (let i = segs.length - 1; i >= 0; i--) {
    const seg = segs[i];
    if (seg.type === 'text' && seg.role === 'final') return seg.content;
  }
  // 回退 1:无 role 标记的老数据,取最后一个 text 段
  for (let i = segs.length - 1; i >= 0; i--) {
    const seg = segs[i];
    if (seg.type === 'text') return seg.content;
  }
  // 回退 2:无 segments,用 message.content
  return props.message.content;
});

// 思考过程段:除最终回复段外的所有段(thought text + 全部 tool)。
// 老数据(无 role):最后一个 text 段是 final,其余是 thought。
const thoughtSegments = computed(() => {
  const segs = segments.value;
  if (segs.length === 0) return [];
  // 找 final 段下标(role=final,或老数据最后一个 text 段)
  let finalIdx = -1;
  for (let i = segs.length - 1; i >= 0; i--) {
    const seg = segs[i];
    if (seg.type === 'text' && seg.role === 'final') { finalIdx = i; break; }
  }
  if (finalIdx === -1) {
    // 老数据:最后一个 text 段当 final
    for (let i = segs.length - 1; i >= 0; i--) {
      if (segs[i].type === 'text') { finalIdx = i; break; }
    }
  }
  if (finalIdx === -1) return segs; // 纯工具:全部算思考
  return segs.slice(0, finalIdx);
});

const hasThought = computed(() => thoughtSegments.value.length > 0);

// 思考步数:思考段里的 tool 段数量(用于「已思考 N 步」文案)。
const thoughtStepCount = computed(
  () => thoughtSegments.value.filter((s) => s.type === 'tool').length,
);

// 思考块折叠状态:流式中(streaming=true)强制展开实时看过程,结束自动收起。
// 用户可手动 toggle(历史消息默认收起,点击展开)。
const thoughtCollapsed = ref(true);
watch(
  () => props.message.streaming,
  (streaming) => {
    // true -> 展开;false -> 收起。仅在 streaming 变化时驱动,不覆盖用户手动操作后的状态。
    thoughtCollapsed.value = !streaming;
  },
  { immediate: true },
);

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
// 思考模式:复制最终回复(finalText),不含思考过程文本。
// 用 navigator.clipboard 优先，浏览器不支持时降级到 textarea + execCommand。
async function handleCopy() {
  const text = finalText.value;
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
          <!-- kind=report:主 Agent 主动汇报子任务结果的消息,用徽标与普通回复区分 -->
          <span
            v-if="message.kind === 'report'"
            class="report-badge"
            title="主 Agent 对子任务结果的汇报"
          >子任务汇报</span>
        </div>
        <div class="assistant-body">
          <!--
            思考模式改造:思考过程(灰色可折叠思考块)+ 最终回复(主气泡)分拆。
            - 思考块:hasThought 时显示「已思考 N 步」按钮条,展开看 thoughtSegments
              (前面的 text 思考文本 + tool 调用)。流式时(streaming)展开实时看过程,
              结束自动收起;历史消息点击按钮条手动展开。
            - 主气泡:finalText(最后一个 text 段)正常 markdown 渲染。
            - 无 segments(老消息)回退渲染 content。
          -->

          <!-- 思考块:有思考过程时显示 -->
          <div v-if="hasThought" class="thought-block">
            <button
              type="button"
              class="thought-toggle"
              :aria-expanded="thoughtCollapsed ? 'false' : 'true'"
              @click="thoughtCollapsed = !thoughtCollapsed"
            >
              <!-- 流式中:旋转齿轮 + 「思考中…」;结束:静态图标 + 「已思考 N 步」 -->
              <svg
                v-if="message.streaming"
                class="thought-spinner"
                width="14" height="14" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
              >
                <path d="M21 12a9 9 0 1 1-6.219-8.56"/>
              </svg>
              <svg
                v-else
                class="thought-icon"
                width="14" height="14" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
              >
                <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9z"/>
              </svg>
              <span class="thought-toggle-label">
                {{ message.streaming ? '思考中…' : `已思考 ${thoughtStepCount} 步` }}
              </span>
              <svg
                class="thought-chevron"
                :class="{ 'is-open': !thoughtCollapsed }"
                width="14" height="14" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
              >
                <path d="m6 9 6 6 6-6"/>
              </svg>
            </button>

            <!-- 思考过程段:展开时按序渲染(text 思考文本小字灰 + tool 调用条) -->
            <div v-show="!thoughtCollapsed" class="thought-content">
              <template v-for="(seg, idx) in thoughtSegments" :key="idx">
                <div
                  v-if="seg.type === 'text'"
                  class="thought-text markdown-body"
                  v-html="renderMarkdown(seg.content)"
                ></div>
                                <div v-else class="tool-segment">
                  <button
                    type="button"
                    class="tool-segment-header"
                    :aria-expanded="seg.expanded ? 'true' : 'false'"
                    @click="toggleExpand(seg)"
                  >
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
            </div>
          </div>

          <!-- 主气泡:最终回复(最后 text 段),无 segments 时回退 content -->
          <div
            v-if="hasSegments"
            class="assistant-content markdown-body"
            v-html="renderMarkdown(finalText)"
          ></div>
          <div v-else class="assistant-content markdown-body" v-html="renderedContent"></div>

          <!-- 操作栏:流式结束后(message.streaming 非 true)且内容非空时显示,
               避免回复未完成时出现复制/点赞按钮(复制到半截内容、对不完整回复反馈都无意义)。
               历史消息无 streaming 字段(falsy),正常显示。 -->
          <div v-if="message.content && !message.streaming" class="assistant-actions">
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

/* kind=report 汇报消息徽标:与普通助手回复区分,提示这是主 Agent 主动汇报的子任务结果 */
.report-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  font-size: 11px;
  font-weight: 500;
  line-height: 1.5;
  color: #2563eb;
  background: rgba(37, 99, 235, 0.08);
  border-radius: 9999px;
  user-select: none;
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

/* ===== 思考块 (思考模式改造) =====
   灰色可折叠容器,包裹 Agent 的思考过程(思考文本 + 工具调用)。
   流式时展开实时显示,结束自动收起;点击「已思考 N 步」按钮条 toggle。
   视觉语言沿用 tool-segment:浅灰底、左边框、小字、低对比度,不打扰主气泡阅读。 */
.thought-block {
  margin: 8px 0 12px 0;
  background: #f9fafb;
  border-left: 2px solid #e5e7eb;
  border-radius: 8px;
  overflow: hidden;
}

.thought-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px 12px;
  background: transparent;
  border: none;
  font-size: 13px;
  color: #6b7280;
  line-height: 1.5;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease;
}

.thought-toggle:hover {
  background: rgba(0, 0, 0, 0.03);
}

.thought-spinner,
.thought-icon {
  flex-shrink: 0;
  color: #9ca3af;
}

.thought-spinner {
  animation: tool-spin 0.8s linear infinite;
}

.thought-toggle-label {
  flex: 1;
}

.thought-chevron {
  flex-shrink: 0;
  color: #9ca3af;
  transition: transform 0.2s ease, color 0.15s ease;
}

.thought-chevron.is-open {
  transform: rotate(180deg);
  color: #6b7280;
}

.thought-content {
  padding: 4px 12px 10px 12px;
  border-top: 1px solid #f0f0f0;
}

/* 思考文本段:小字、浅灰,与主气泡正文区分 */
.thought-text {
  font-size: 13px;
  line-height: 1.6;
  color: #6b7280;
  margin: 6px 0;
}

/* 思考块内的 tool 段去掉外层 margin,贴合思考块内边距 */
.thought-content :deep(.tool-segment) {
  margin: 6px 0;
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
