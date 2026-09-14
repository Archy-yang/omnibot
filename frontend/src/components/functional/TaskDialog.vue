<script setup lang="ts">
/**
 * TaskDialog — 任务中心弹窗
 *
 * 两级视图:
 * - 列表页:全部后台任务(倒序),状态徽标 + 短名(回落 goal) + #id + 时间,点击进详情
 * - 详情页:任务信息卡(目标/状态/耗时) → 执行流水(每步默认收起,点击展开看请求/响应)
 *
 * 执行流水步骤复用聊天里 tool-segment 的视觉语言(徽标+单行预览+展开旋转箭头)。
 * 运行中任务详情页 5s 轻量轮询刷新(WS 只通知 completed,页面打开期间的进度感知靠它)。
 */
import { ref, watch, computed, onBeforeUnmount } from 'vue';
import { marked } from 'marked';
import DOMPurify from 'dompurify';
import DialogShell from '@/components/layout/DialogShell.vue';
import { agentTaskService } from '@/services/agentTask';
import { useToast } from '@/composables/useToast';
import type { AgentTaskItem, AgentTaskDetail, AgentTaskStep } from '@/types/api';

marked.use({ async: false });

const props = defineProps<{
  visible: boolean;
}>();

const emit = defineEmits<{
  close: [];
}>();

const { error: toastError } = useToast();

// ===== 视图状态 =====
const view = ref<'list' | 'detail'>('list');
const tasks = ref<AgentTaskItem[]>([]);
const detail = ref<AgentTaskDetail | null>(null);
const steps = ref<AgentTaskStep[]>([]);
const isLoadingList = ref(false);
const isLoadingDetail = ref(false);

// 展开的步骤 seq 集合(默认全收起,点击展开)
const expandedSeqs = ref<Set<number>>(new Set());

const loadList = async () => {
  isLoadingList.value = true;
  try {
    tasks.value = await agentTaskService.listAll();
  } catch (err) {
    toastError(err instanceof Error ? err.message : '加载任务失败');
  } finally {
    isLoadingList.value = false;
  }
};

// 有旧数据时原地静默刷新(不闪加载态);只有首开无数据才显示加载中
const hasListData = computed(() => tasks.value.length > 0);

watch(
  () => props.visible,
  (v) => {
    if (v) {
      view.value = 'list';
      detail.value = null;
      steps.value = [];
      loadList();
    } else {
      stopDetailPolling();
    }
  }
);

// ===== 详情 =====
const openDetail = async (taskId: number) => {
  view.value = 'detail';
  isLoadingDetail.value = true;
  expandedSeqs.value = new Set();
  try {
    detail.value = await agentTaskService.getDetail(taskId);
    steps.value = await agentTaskService.listSteps(taskId);
  } catch (err) {
    toastError(err instanceof Error ? err.message : '加载任务详情失败');
    view.value = 'list';
  } finally {
    isLoadingDetail.value = false;
  }
};

const backToList = () => {
  stopDetailPolling();
  view.value = 'list';
  detail.value = null;
  steps.value = [];
  loadList();
};

// 运行中任务 5s 轮询刷新(详情页打开期间);完成/失败后刷一次流水并停止
let pollTimer: ReturnType<typeof setInterval> | null = null;
const isActiveStatus = (s: string | undefined) =>
  s === 'pending' || s === 'running' || s === 'input_required';

const stopDetailPolling = () => {
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
};

watch(
  () => detail.value?.status,
  (status) => {
    stopDetailPolling();
    if (props.visible && view.value === 'detail' && detail.value && isActiveStatus(status)) {
      const taskId = detail.value.id;
      pollTimer = setInterval(async () => {
        try {
          detail.value = await agentTaskService.getDetail(taskId);
          if (!isActiveStatus(detail.value?.status)) {
            steps.value = await agentTaskService.listSteps(taskId);
            stopDetailPolling();
          }
        } catch {
          // 轮询失败静默,下轮再试
        }
      }, 5_000);
    }
  },
  { immediate: true }
);

onBeforeUnmount(stopDetailPolling);

// ===== 展示辅助 =====
const toggleStep = (seq: number) => {
  const next = new Set(expandedSeqs.value);
  if (next.has(seq)) next.delete(seq);
  else next.add(seq);
  expandedSeqs.value = next;
};

const statusLabel = (s: string): string =>
  ({
    pending: '排队中',
    running: '执行中',
    completed: '已完成',
    failed: '失败',
    cancelled: '已取消',
    input_required: '待输入',
  })[s] ?? s;

const displayName = (t: { name: string; goal: string }): string => {
  if (t.name) return t.name;
  const g = t.goal.replace(/\s+/g, ' ').trim();
  return g.length > 18 ? g.slice(0, 18) + '…' : g || '未命名任务';
};

const isActive = (s: string) => isActiveStatus(s);

// llm_call 的 request 是完整 messages JSON(动辄几十 KB),只给截断预览
const llmRequestPreview = (raw: string): string =>
  raw.length > 800 ? raw.slice(0, 800) + '\n…(请求过长,仅展示前 800 字符)' : raw;

const prettyJSON = (raw: string): string => {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
};

// artifact(子 Agent 报告)按 markdown 渲染,与聊天气泡同款管线
const renderMarkdown = (text: string): string =>
  DOMPurify.sanitize(marked.parse(text, { breaks: true, gfm: true, async: false }) as string);

const artifactHtml = computed(() =>
  detail.value?.artifact ? renderMarkdown(detail.value.artifact) : ''
);

const stepStatusClass = (s: string) =>
  ({ success: 'is-ok', error: 'is-err', not_found: 'is-err' })[s] ?? '';
</script>

<template>
  <DialogShell :visible="visible" title="任务" width="760px" height="min(640px, calc(100vh - 48px))" @close="emit('close')">
    <!-- ===== 列表页 ===== -->
    <template v-if="view === 'list'">
      <div v-if="isLoadingList && !hasListData" class="task-loading">加载中…</div>

      <div v-else-if="tasks.length === 0" class="task-empty">
        <p class="task-empty-title">还没有派过任务</p>
        <p class="task-empty-hint">在对话里让我「查查某领域的最新动态」，我就会派后台任务去办；完成后自动汇报，这里能看到每次的执行流水。</p>
      </div>

      <div v-else class="task-list" :class="{ 'is-refreshing': isLoadingList }">
        <button
          v-for="t in tasks"
          :key="t.id"
          type="button"
          class="task-row"
          @click="openDetail(t.id)"
        >
          <span class="task-dot" :class="`dot-${t.status}`">
            <svg v-if="isActive(t.status)" class="task-spin" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
              <path d="M21 12a9 9 0 1 1-6.219-8.56"/>
            </svg>
          </span>
          <span class="task-row-main">
            <span class="task-row-name">{{ displayName(t) }}</span>
            <span class="task-row-goal">{{ t.goal }}</span>
          </span>
          <span class="task-row-status" :class="`stx-${t.status}`">{{ statusLabel(t.status) }}</span>
          <span class="task-row-time">{{ t.finished_at || t.created_at }}</span>
          <span class="task-row-id">#{{ t.id }}</span>
          <svg class="task-row-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="m9 18 6-6-6-6"/>
          </svg>
        </button>
      </div>
    </template>

    <!-- ===== 详情页 ===== -->
    <template v-else>
      <div v-if="isLoadingDetail || !detail" class="task-loading">加载中…</div>
      <div v-else class="task-detail">
        <!-- 返回 -->
        <button type="button" class="task-back" @click="backToList">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="m15 18-6-6 6-6"/>
          </svg>
          全部任务
        </button>

        <!-- 信息卡 -->
        <div class="task-head">
          <div class="task-head-title">
            <span class="task-head-name">{{ displayName(detail) }}</span>
            <span class="task-status" :class="`st-${detail.status}`">
              <svg v-if="isActive(detail.status)" class="task-spin" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
                <path d="M21 12a9 9 0 1 1-6.219-8.56"/>
              </svg>
              {{ statusLabel(detail.status) }}
            </span>
            <span class="task-row-id">#{{ detail.id }}</span>
          </div>
          <div class="task-head-goal">{{ detail.goal }}</div>
          <div class="task-head-times">
            <span>创建 {{ detail.created_at }}</span>
            <span v-if="detail.started_at">开始 {{ detail.started_at }}</span>
            <span v-if="detail.completed_at">完成 {{ detail.completed_at }}</span>
          </div>
        </div>

        <!-- 执行流水 -->
        <div class="steps-title">执行流水（{{ steps.length }} 步）</div>
        <div v-if="steps.length === 0" class="steps-empty">
          {{ isActive(detail.status) ? '还没有执行记录，等运行起来再来看。' : '没有执行记录。' }}
        </div>
        <div v-else class="steps">
          <div v-for="s in steps" :key="s.seq" class="step" :class="{ 'is-open': expandedSeqs.has(s.seq) }">
            <button type="button" class="step-header" :aria-expanded="expandedSeqs.has(s.seq) ? 'true' : 'false'" @click="toggleStep(s.seq)">
              <span class="step-badge" :class="s.kind === 'llm_call' ? 'badge-llm' : 'badge-tool'">
                {{ s.kind === 'llm_call' ? '模型' : '工具' }}
              </span>
              <span class="step-name">{{ s.kind === 'llm_call' ? (s.model || 'LLM') : s.tool }}</span>
              <span v-if="s.kind === 'llm_call'" class="step-preview">第 {{ s.seq + 1 }} 轮推理</span>
              <span v-else class="step-preview">{{ s.request }}</span>
              <span class="step-status" :class="stepStatusClass(s.status)">
                {{ s.status === 'success' ? '成功' : s.status === 'not_found' ? '不存在' : '失败' }}
              </span>
              <span class="step-duration">{{ s.duration_ms }}ms</span>
              <svg class="step-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="m6 9 6 6 6-6"/>
              </svg>
            </button>
            <div v-show="expandedSeqs.has(s.seq)" class="step-body">
              <template v-if="s.kind === 'tool_call'">
                <div class="step-section-label">参数</div>
                <pre class="step-raw">{{ prettyJSON(s.request) }}</pre>
                <div class="step-section-label">结果</div>
                <pre class="step-raw step-result">{{ s.response }}</pre>
              </template>
              <template v-else>
                <div class="step-section-label">请求（截断预览）</div>
                <pre class="step-raw">{{ llmRequestPreview(prettyJSON(s.request)) }}</pre>
                <div class="step-section-label">响应</div>
                <pre class="step-raw step-result">{{ prettyJSON(s.response) }}</pre>
              </template>
            </div>
          </div>
        </div>

        <!-- 结果 -->
        <template v-if="detail.artifact">
          <div class="steps-title">任务结果</div>
          <div class="task-artifact markdown-body" v-html="artifactHtml"></div>
        </template>
        <div v-if="detail.error_msg" class="steps-title">失败原因</div>
        <div v-if="detail.error_msg" class="task-error">{{ detail.error_msg }}</div>
      </div>
    </template>
  </DialogShell>
</template>

<style scoped>
.task-loading,
.task-empty {
  padding: 40px 0;
  text-align: center;
  font-size: 13px;
  color: var(--label-tertiary);
}

.task-empty-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--label-primary);
  margin-bottom: 6px;
}

.task-empty-hint {
  font-size: 13px;
  color: var(--label-tertiary);
  line-height: 1.6;
  max-width: 420px;
  margin: 0 auto;
}

/* ===== 状态徽标 ===== */
.task-status {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex: none;
  padding: 2px 8px;
  font-size: 11px;
  line-height: 1.5;
  border-radius: 9999px;
  white-space: nowrap;
}

.task-status.st-running,
.task-status.st-pending,
.task-status.st-input_required {
  color: var(--accent);
  background: rgba(65, 118, 230, 0.1);
}

.task-status.st-completed {
  color: #0a7d33;
  background: rgba(16, 163, 74, 0.12);
}

.task-status.st-failed {
  color: var(--error);
  background: var(--error-bg);
}

.task-status.st-cancelled {
  color: var(--label-tertiary);
  background: var(--bg-active);
}

.task-spin {
  animation: task-rotate 0.8s linear infinite;
}

@keyframes task-rotate {
  to { transform: rotate(360deg); }
}

/* ===== 列表:dsh 式清单——无框盒,发丝线分隔,状态圆点+轻文字 ===== */
.task-list {
  display: flex;
  flex-direction: column;
  transition: opacity 150ms ease;
}

.task-list.is-refreshing {
  opacity: 0.6; /* 原地刷新:数据未到不闪加载态,整列轻微降不透明度示意 */
}

.task-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 12px 10px;
  border: none;
  border-bottom: 0.5px solid var(--border-l1);
  border-radius: 10px;
  background: transparent;
  text-align: left;
  font-family: inherit;
  cursor: pointer;
  transition: background 150ms ease;
}

.task-row:last-child {
  border-bottom: none;
}

.task-row:hover {
  background: var(--bg-hover);
}

/* 状态圆点:执行中=主题蓝转圈,完成=绿,失败=红,取消/排队=灰 */
.task-dot {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
}

.task-dot::before {
  content: "";
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--label-caption);
}

.task-dot.dot-completed::before {
  background: #10a34a;
}

.task-dot.dot-failed::before {
  background: var(--error);
}

.task-dot.dot-running::before,
.task-dot.dot-pending::before,
.task-dot.dot-input_required::before {
  content: none; /* 运行态不画圆点,位置让给旋转图标 */
}

.task-dot.dot-running,
.task-dot.dot-pending,
.task-dot.dot-input_required {
  color: var(--accent);
}

.task-dot .task-spin {
  display: block;
}

.task-row-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.task-row-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--label-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-row-goal {
  font-size: 12px;
  color: var(--label-caption);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-row-status {
  flex: none;
  font-size: 12px;
  color: var(--label-tertiary);
}

.task-row-status.stx-running,
.task-row-status.stx-pending,
.task-row-status.stx-input_required {
  color: var(--accent);
}

.task-row-status.stx-completed {
  color: #0a7d33;
}

.task-row-status.stx-failed {
  color: var(--error);
}

.task-row-time {
  flex: none;
  font-size: 12px;
  color: var(--label-caption);
  font-variant-numeric: tabular-nums;
}

.task-row-id {
  flex: none;
  font-size: 12px;
  color: var(--label-caption);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.task-row-chevron {
  flex: none;
  color: var(--label-caption);
  opacity: 0;
  transition: opacity 150ms ease, transform 150ms ease;
}

.task-row:hover .task-row-chevron {
  opacity: 1;
}

/* ===== 详情 ===== */
.task-back {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: none;
  background: transparent;
  padding: 4px 8px 4px 0;
  font-size: 13px;
  font-family: inherit;
  color: var(--label-tertiary);
  cursor: pointer;
  border-radius: 8px;
  margin-bottom: 10px;
}

.task-back:hover {
  color: var(--label-secondary);
}

.task-head {
  padding: 12px 14px;
  border: 0.5px solid var(--border-l2);
  border-radius: 12px;
  background: var(--bg-tip);
  margin-bottom: 16px;
}

.task-head-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.task-head-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--label-primary);
}

.task-head-goal {
  font-size: 13px;
  color: var(--label-secondary);
  line-height: 1.6;
  margin-bottom: 8px;
  word-break: break-word;
}

.task-head-times {
  display: flex;
  gap: 14px;
  font-size: 12px;
  color: var(--label-caption);
}

/* ===== 执行流水 ===== */
.steps-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--label-secondary);
  margin: 4px 0 8px 0;
}

.steps-empty {
  font-size: 13px;
  color: var(--label-tertiary);
  padding: 8px 0 4px 0;
}

.steps {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 16px;
}

.step {
  border: 0.5px solid var(--border-l2);
  border-radius: 10px;
  overflow: hidden;
}

.step.is-open {
  background: var(--bg-tip);
}

.step-header {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 8px 10px;
  border: none;
  background: transparent;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  transition: background 150ms ease;
}

.step-header:hover {
  background: var(--bg-hover);
}

.step-badge {
  flex: none;
  padding: 1px 6px;
  font-size: 11px;
  border-radius: 4px;
}

.badge-tool {
  color: var(--accent);
  background: rgba(65, 118, 230, 0.1);
}

.badge-llm {
  color: var(--label-tertiary);
  background: var(--bg-active);
}

.step-name {
  flex: none;
  font-size: 13px;
  font-weight: 500;
  color: var(--label-primary);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.step-preview {
  flex: 1;
  min-width: 0;
  font-size: 12px;
  color: var(--label-caption);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.step-status {
  flex: none;
  font-size: 11px;
  color: var(--label-tertiary);
}

.step-status.is-ok {
  color: #0a7d33;
}

.step-status.is-err {
  color: var(--error);
}

.step-duration {
  flex: none;
  font-size: 11px;
  color: var(--label-caption);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.step-chevron {
  flex: none;
  color: var(--label-caption);
  transition: transform 0.15s ease;
}

.step.is-open .step-chevron {
  transform: rotate(180deg);
}

.step-body {
  padding: 4px 12px 10px 12px;
  border-top: 1px solid var(--border-l1);
}

.step-section-label {
  font-size: 11px;
  color: var(--label-caption);
  margin: 8px 0 2px 0;
}

.step-raw {
  margin: 0;
  padding: 8px 10px;
  max-height: 200px;
  overflow-y: auto;
  background: var(--code-bg);
  border-radius: 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  line-height: 1.6;
  color: var(--label-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}

.step-raw.step-result {
  max-height: 300px;
}

/* ===== 结果 ===== */
.task-artifact {
  padding: 12px 14px;
  border: 0.5px solid var(--border-l2);
  border-radius: 12px;
  font-size: 14px;
  line-height: 1.75;
  color: var(--label-primary);
  word-break: break-word;
}

.task-error {
  padding: 12px 14px;
  border: 0.5px solid var(--error-bg);
  border-radius: 12px;
  background: var(--error-bg);
  font-size: 13px;
  line-height: 1.6;
  color: var(--error);
  word-break: break-word;
}
</style>
