<script setup lang="ts">
/**
 * TaskChip — 消息底部的任务卡片
 *
 * 显示任务短名(回落 goal 摘要)+ 实时状态;点击冒泡 open-task,
 * 由页面打开任务中心并直达该任务的执行链详情。
 * 挂载时自取一次详情(每条消息通常只有 1 个任务,请求量可忽略);
 * 运行中任务 5s 轮询刷新状态。
 */
import { ref, watch, onBeforeUnmount } from 'vue';
import { agentTaskService } from '@/services/agentTask';
import type { AgentTaskDetail } from '@/types/api';

const props = defineProps<{
  taskId: number;
}>();

const emit = defineEmits<{
  'open-task': [taskId: number];
}>();

const detail = ref<AgentTaskDetail | null>(null);
const failed = ref(false);

const load = async () => {
  try {
    detail.value = await agentTaskService.getDetail(props.taskId);
    failed.value = false;
  } catch {
    failed.value = true;
  }
};

load();

// 运行中任务跟随状态变化(完成即停)
let pollTimer: ReturnType<typeof setInterval> | null = null;
const stopPoll = () => {
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
};
watch(
  () => detail.value?.status,
  (s) => {
    stopPoll();
    if (s === 'pending' || s === 'running' || s === 'input_required') {
      pollTimer = setInterval(load, 5_000);
    }
  },
  { immediate: true }
);
onBeforeUnmount(stopPoll);

const statusLabel = (s: string): string =>
  ({
    pending: '排队中',
    running: '执行中',
    completed: '已完成',
    failed: '失败',
    cancelled: '已取消',
    input_required: '待输入',
  })[s] ?? s;

const displayName = (d: AgentTaskDetail): string => {
  if (d.name) return d.name;
  const g = d.goal.replace(/\s+/g, ' ').trim();
  return g.length > 14 ? g.slice(0, 14) + '…' : g || `任务 #${d.id}`;
};
</script>

<template>
  <button
    v-if="!failed && detail"
    type="button"
    class="task-chip"
    :title="`查看任务 #${detail.id} 的执行链`"
    @click="emit('open-task', detail.id)"
  >
    <svg class="task-chip-icon" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <path d="M9 11l3 3L22 4"/>
      <path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>
    </svg>
    <span class="task-chip-name">{{ displayName(detail) }}</span>
    <span class="task-chip-status" :class="`st-${detail.status}`">{{ statusLabel(detail.status) }}</span>
    <span class="task-chip-id">#{{ detail.id }}</span>
  </button>
</template>

<style scoped>
.task-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  margin-top: 8px;
  padding: 5px 12px;
  border: 0.5px solid var(--border-l2);
  border-radius: 9999px;
  background: transparent;
  font-family: inherit;
  cursor: pointer;
  transition: background 150ms ease, border-color 150ms ease;
}

.task-chip:hover {
  background: var(--bg-hover);
  border-color: var(--accent);
}

.task-chip-icon {
  flex: none;
  color: var(--accent);
}

.task-chip-name {
  font-size: 12px;
  font-weight: 500;
  color: var(--label-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-chip-status {
  flex: none;
  font-size: 11px;
  color: var(--label-tertiary);
}

.task-chip-status.st-running,
.task-chip-status.st-pending,
.task-chip-status.st-input_required {
  color: var(--accent);
}

.task-chip-status.st-completed {
  color: #0a7d33;
}

.task-chip-status.st-failed {
  color: var(--error);
}

.task-chip-id {
  flex: none;
  font-size: 11px;
  color: var(--label-caption);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
</style>
