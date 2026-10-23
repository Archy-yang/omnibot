<script setup lang="ts">
/**
 * JournalDrawer — 个人经历抽屉(21 号 J3)
 *
 * 双 tab:
 *   - 时间轴:Journal 按月分组,条目可展开溯源片段,可编辑/删除(复用记忆 CRUD)
 *   - 档案:主题卡片列表 → 详情演进时间线;state_desc/status 可纠正
 *
 * 视觉对齐 MemoryDrawer 同款范式(DialogShell + scoped 样式,不用 NaiveUI)。
 * 全部为抽屉内部状态,不进 Store(单一组件使用,宪章 §2.3)。
 */
import { computed, onMounted, ref, watch } from 'vue';
import DialogShell from '@/components/layout/DialogShell.vue';
import { useToast } from '@/composables/useToast';
import { journalService } from '@/services/journal';
import { memoryService } from '@/services/memory';
import type { JournalItem, MatterDetail, MatterSummary } from '@/types/api';

const props = defineProps<{
  visible: boolean;
}>();

const emit = defineEmits<{
  close: [];
}>();

const { success, error } = useToast();

const getErrorMessage = (err: unknown): string =>
  err instanceof Error ? err.message : '服务暂时不可用,请稍后再试。';

// ===== tab =====
type JournalTab = 'timeline' | 'matters';
const activeTab = ref<JournalTab>('timeline');
const handleSwitchTab = (tab: JournalTab): void => {
  if (activeTab.value === tab) return;
  activeTab.value = tab;
  expandedId.value = null;
  detail.value = null;
};

// ===== 时间轴 =====
const items = ref<JournalItem[]>([]);
const nextCursor = ref('');
const isLoading = ref(false);
const isLoadingMore = ref(false);
const kindFilter = ref<'all' | 'fact' | 'loop'>('all');

const loadJournal = async (): Promise<void> => {
  isLoading.value = true;
  try {
    const page = await journalService.getJournal({ kind: kindFilter.value, limit: 50 });
    items.value = page.items;
    nextCursor.value = page.next_cursor;
  } catch (err) {
    error(getErrorMessage(err));
  } finally {
    isLoading.value = false;
  }
};

const loadMore = async (): Promise<void> => {
  if (!nextCursor.value || isLoadingMore.value) return;
  isLoadingMore.value = true;
  try {
    const page = await journalService.getJournal({
      kind: kindFilter.value,
      cursor: nextCursor.value,
      limit: 50,
    });
    items.value = [...items.value, ...page.items];
    nextCursor.value = page.next_cursor;
  } catch (err) {
    error(getErrorMessage(err));
  } finally {
    isLoadingMore.value = false;
  }
};

const handleSwitchKind = (kind: 'all' | 'fact' | 'loop'): void => {
  if (kindFilter.value === kind) return;
  kindFilter.value = kind;
  void loadJournal();
};

/** 有效时间:事件时间优先,回落沉淀时间 */
const effectiveDate = (item: JournalItem): Date =>
  new Date(item.happened_at ?? item.created_at);

/** 按月分组(有效时间),组内已按后端降序 */
const monthGroups = computed<Array<{ key: string; label: string; items: JournalItem[] }>>(() => {
  const map = new Map<string, JournalItem[]>();
  for (const it of items.value) {
    const d = effectiveDate(it);
    const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
    const bucket = map.get(key);
    if (bucket) bucket.push(it);
    else map.set(key, [it]);
  }
  return Array.from(map.entries()).map(([key, list]) => ({
    key,
    label: key.replace('-', ' 年 ') + ' 月',
    items: list,
  }));
});

const isHappened = (item: JournalItem): boolean => !!item.happened_at;
const formatDay = (item: JournalItem): string => {
  const d = effectiveDate(item);
  const pad = (n: number): string => String(n).padStart(2, '0');
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
};
const formatFull = (iso: string): string => {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number): string => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

const kindLabel = (item: JournalItem): string => (item.kind === 'loop' ? '待办' : '经历');

// 28号 N3:目标日剩余天数(按自然日差;负数 = 已过期天数)
const targetDays = (iso: string): number => {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return 0;
  const today = new Date();
  const utcToday = Date.UTC(today.getFullYear(), today.getMonth(), today.getDate());
  const utcTarget = Date.UTC(d.getFullYear(), d.getMonth(), d.getDate());
  return Math.round((utcTarget - utcToday) / 86400000);
};

// 条目展开:溯源 + 编辑/删除
const expandedId = ref<number | null>(null);
const toggleExpand = (id: number): void => {
  expandedId.value = expandedId.value === id ? null : id;
};

const editingId = ref<number | null>(null);
const editingContent = ref('');
const startEditing = (item: JournalItem): void => {
  editingId.value = item.id;
  editingContent.value = item.content;
};
const cancelEditing = (): void => {
  editingId.value = null;
  editingContent.value = '';
};
const editingLength = computed(() => [...editingContent.value.trim()].length);

const handleUpdateItem = async (): Promise<void> => {
  if (editingId.value === null) return;
  const content = editingContent.value.trim();
  if (!content) {
    error('内容不能为空。');
    return;
  }
  if (editingLength.value > 200) {
    error('这条记忆太长了,请控制在 200 字以内。');
    return;
  }
  try {
    await memoryService.updateMemory(editingId.value, { content });
    const it = items.value.find((x) => x.id === editingId.value);
    if (it) it.content = content;
    if (detail.value) {
      const dt = detail.value.timeline.find((x) => x.id === it?.id);
      if (dt) dt.content = content;
    }
    cancelEditing();
    success('已更新。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

const handleDeleteItem = async (id: number): Promise<void> => {
  if (!window.confirm('确定要删除这条记录吗?删除后助手不再记住它。')) return;
  try {
    await memoryService.deleteMemory(id);
    items.value = items.value.filter((x) => x.id !== id);
    if (detail.value) detail.value.timeline = detail.value.timeline.filter((x) => x.id !== id);
    success('已删除。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

const jumpToMatter = (matterId: number): void => {
  activeTab.value = 'matters';
  expandedId.value = null;
  void openMatter(matterId);
};

// ===== 档案 =====
const matters = ref<MatterSummary[]>([]);
const isLoadingMatters = ref(false);
const matterStatusFilter = ref<'active' | 'done' | 'archived' | ''>('');
const detail = ref<MatterDetail | null>(null);
const isLoadingDetail = ref(false);

const statusLabel = (s: string): string =>
  s === 'done' ? '已完成' : s === 'archived' ? '已归档' : '进行中';

const loadMatters = async (): Promise<void> => {
  isLoadingMatters.value = true;
  try {
    matters.value = await journalService.listMatters(matterStatusFilter.value || undefined);
  } catch (err) {
    error(getErrorMessage(err));
  } finally {
    isLoadingMatters.value = false;
  }
};

const handleSwitchStatus = (s: 'active' | 'done' | 'archived' | ''): void => {
  if (matterStatusFilter.value === s) return;
  matterStatusFilter.value = s;
  void loadMatters();
};

const openMatter = async (id: number): Promise<void> => {
  isLoadingDetail.value = true;
  try {
    detail.value = await journalService.getMatter(id);
  } catch (err) {
    error(getErrorMessage(err));
  } finally {
    isLoadingDetail.value = false;
  }
};

const backToList = (): void => {
  detail.value = null;
  void loadMatters();
};

// 档案纠正
const editingDesc = ref('');
const isEditingDesc = ref(false);
const startEditDesc = (): void => {
  if (!detail.value) return;
  editingDesc.value = detail.value.state_desc;
  isEditingDesc.value = true;
};
const isSavingDesc = ref(false);
const handleSaveDesc = async (): Promise<void> => {
  if (!detail.value) return;
  isSavingDesc.value = true;
  try {
    await journalService.updateMatter(detail.value.id, { state_desc: editingDesc.value.trim() });
    detail.value.state_desc = editingDesc.value.trim();
    isEditingDesc.value = false;
    success('已更新档案状态。');
  } catch (err) {
    error(getErrorMessage(err));
  } finally {
    isSavingDesc.value = false;
  }
};

// ===== 文档同步(23号) =====
const isSyncingDoc = ref(false);
const handleSyncDoc = async (): Promise<void> => {
  if (!detail.value || isSyncingDoc.value) return;
  if (detail.value.doc_url && !window.confirm('将用 OmniBot 中的当前内容覆盖飞书文档(手动编辑会丢失),继续?')) return;
  isSyncingDoc.value = true;
  try {
    const res = await journalService.syncMatterDoc(detail.value.id);
    detail.value.doc_url = res.doc_url;
    detail.value.synced_at = res.synced_at;
    success(res.created ? '已创建飞书文档。' : '已更新飞书文档。');
  } catch (err) {
    error(getErrorMessage(err));
  } finally {
    isSyncingDoc.value = false;
  }
};

const isChangingStatus = ref(false);
const handleMatterStatus = async (status: string): Promise<void> => {
  if (!detail.value || detail.value.status === status) return;
  isChangingStatus.value = true;
  try {
    await journalService.updateMatter(detail.value.id, { status });
    detail.value.status = status;
    success('已更新档案状态。');
  } catch (err) {
    error(getErrorMessage(err));
  } finally {
    isChangingStatus.value = false;
  }
};

// 抽屉打开时拉数据;首次挂载兜底
watch(
  () => props.visible,
  (v) => {
    if (!v) return;
    expandedId.value = null;
    editingId.value = null;
    detail.value = null;
    void loadJournal();
    void loadMatters();
  }
);
onMounted(() => {
  if (props.visible) {
    void loadJournal();
    void loadMatters();
  }
});
</script>

<template>
  <DialogShell :visible="visible" title="个人经历" width="720px" @close="emit('close')">
    <!-- 提醒条 -->
    <div class="safety-bar">
      <span class="safety-text">这里沉淀自你的长期对话——每条记录都来自真实聊天,可纠正、可删除</span>
    </div>

    <!-- 双 tab -->
    <div class="journal-tabs">
      <button
        type="button"
        class="journal-tab"
        :class="{ active: activeTab === 'timeline' }"
        @click="handleSwitchTab('timeline')"
      >
        时间轴
      </button>
      <button
        type="button"
        class="journal-tab"
        :class="{ active: activeTab === 'matters' }"
        @click="handleSwitchTab('matters')"
      >
        档案
        <span v-if="matters.length" class="tab-count">{{ matters.length }}</span>
      </button>
    </div>

    <!-- ===== 时间轴 ===== -->
    <div v-if="activeTab === 'timeline'" class="tab-body">
      <div class="filter-row">
        <button
          v-for="k in (['all', 'fact', 'loop'] as const)"
          :key="k"
          type="button"
          class="filter-chip"
          :class="{ active: kindFilter === k }"
          @click="handleSwitchKind(k)"
        >
          {{ k === 'all' ? '全部' : k === 'fact' ? '经历' : '待办' }}
        </button>
      </div>

      <div v-if="isLoading" class="empty-hint">加载中...</div>
      <div v-else-if="monthGroups.length === 0" class="empty-hint">
        还没有沉淀的个人经历。正常聊天即可,值得记住的事会被自动整理到这里。
      </div>

      <template v-else>
        <section v-for="group in monthGroups" :key="group.key" class="month-group">
          <h3 class="month-label">{{ group.label }}</h3>
          <article
            v-for="item in group.items"
            :key="item.id"
            class="journal-card"
          >
            <div class="card-head" @click="toggleExpand(item.id)">
              <span class="card-date">{{ formatDay(item) }}</span>
              <span class="card-kind" :class="item.kind">{{ kindLabel(item) }}</span>
              <span v-if="item.pinned" class="card-pin" title="已置顶">★</span>
              <span class="card-content">{{ item.content }}</span>
            </div>

            <div v-if="!isHappened(item)" class="card-sub">沉淀于 {{ formatFull(item.created_at) }}</div>

            <div v-if="expandedId === item.id" class="card-expand">
              <div v-if="item.matter_id" class="expand-row">
                <span class="expand-label">所属档案</span>
                <button type="button" class="link-btn" @click.stop="jumpToMatter(item.matter_id)">查看档案 →</button>
              </div>
              <div class="expand-row">
                <span class="expand-label">来源对话</span>
                <span v-if="item.links.length === 0" class="expand-muted">原始消息已不可见</span>
                <ul v-else class="link-list">
                  <li v-for="l in item.links" :key="l.message_id" class="link-item">
                    <span class="link-time">{{ formatFull(l.occurred_at) }}</span>
                    <span class="link-snippet">{{ l.snippet }}{{ l.snippet.length >= 80 ? '…' : '' }}</span>
                  </li>
                </ul>
              </div>
              <div class="expand-actions">
                <button type="button" class="mini-btn" @click.stop="startEditing(item)">编辑</button>
                <button type="button" class="mini-btn danger" @click.stop="handleDeleteItem(item.id)">删除</button>
              </div>
              <div v-if="editingId === item.id" class="edit-area">
                <textarea v-model="editingContent" class="edit-textarea" rows="3" :maxlength="220"></textarea>
                <div class="edit-actions">
                  <button type="button" class="mini-btn primary" @click.stop="handleUpdateItem">保存</button>
                  <button type="button" class="mini-btn" @click.stop="cancelEditing">取消</button>
                </div>
              </div>
            </div>
          </article>
        </section>

        <button v-if="nextCursor" type="button" class="load-more" :disabled="isLoadingMore" @click="loadMore">
          {{ isLoadingMore ? '加载中...' : '加载更早' }}
        </button>
      </template>
    </div>

    <!-- ===== 档案 ===== -->
    <div v-else class="tab-body">
      <!-- 详情视图 -->
      <template v-if="detail">
        <button type="button" class="back-btn" @click="backToList">← 返回档案列表</button>
        <div class="matter-detail">
          <h3 class="matter-title">{{ detail.title }}</h3>
          <div class="matter-meta">
            <span class="status-chip" :class="detail.status">{{ statusLabel(detail.status) }}</span>
            <!-- 28号 N3:目标日徽标(临近/过期红色提示) -->
            <span v-if="detail.target_at" class="target-chip" :class="{ 'target-urgent': targetDays(detail.target_at) <= 3 }">
              🎯 {{ formatFull(detail.target_at).slice(0, 10) }}{{ targetDays(detail.target_at) >= 0 ? ` · 剩 ${targetDays(detail.target_at)} 天` : ` · 已过期 ${-targetDays(detail.target_at)} 天` }}
            </span>
            <span class="matter-count">{{ detail.memory_count }} 条记录</span>
            <button
              type="button"
              class="sync-btn"
              :disabled="isSyncingDoc"
              @click="handleSyncDoc"
            >
              {{ isSyncingDoc ? '同步中...' : (detail.doc_url ? '更新到飞书文档' : '同步到飞书') }}
            </button>
          </div>
          <div v-if="detail.doc_url" class="doc-link-row">
            <a :href="detail.doc_url" target="_blank" rel="noopener" class="doc-link">打开飞书文档 ↗</a>
            <span v-if="detail.synced_at" class="doc-synced">上次同步 {{ formatFull(detail.synced_at) }}</span>
          </div>

          <div class="desc-section">
            <div class="desc-head">
              <span class="expand-label">当前状态</span>
              <button v-if="!isEditingDesc" type="button" class="mini-btn" @click="startEditDesc">纠正</button>
            </div>
            <p v-if="!isEditingDesc" class="desc-text">{{ detail.state_desc || '(暂无)' }}</p>
            <div v-else class="edit-area">
              <textarea v-model="editingDesc" class="edit-textarea" rows="3"></textarea>
              <div class="edit-actions">
                <button type="button" class="mini-btn primary" :disabled="isSavingDesc" @click="handleSaveDesc">保存</button>
                <button type="button" class="mini-btn" @click="isEditingDesc = false">取消</button>
              </div>
            </div>
          </div>

          <div class="status-section">
            <span class="expand-label">档案状态</span>
            <button
              v-for="s in (['active', 'done', 'archived'] as const)"
              :key="s"
              type="button"
              class="filter-chip"
              :class="{ active: detail.status === s }"
              :disabled="isChangingStatus"
              @click="handleMatterStatus(s)"
            >
              {{ statusLabel(s) }}
            </button>
          </div>

          <h4 class="timeline-label">演进时间线</h4>
          <article v-for="item in detail.timeline" :key="item.id" class="journal-card">
            <div class="card-head" @click="toggleExpand(item.id)">
              <span class="card-date">{{ formatDay(item) }}</span>
              <span class="card-kind" :class="item.kind">{{ kindLabel(item) }}</span>
              <span class="card-content">{{ item.content }}</span>
            </div>
            <div v-if="!isHappened(item)" class="card-sub">沉淀于 {{ formatFull(item.created_at) }}</div>
            <div v-if="expandedId === item.id" class="card-expand">
              <div class="expand-row">
                <span class="expand-label">来源对话</span>
                <span v-if="item.links.length === 0" class="expand-muted">原始消息已不可见</span>
                <ul v-else class="link-list">
                  <li v-for="l in item.links" :key="l.message_id" class="link-item">
                    <span class="link-time">{{ formatFull(l.occurred_at) }}</span>
                    <span class="link-snippet">{{ l.snippet }}{{ l.snippet.length >= 80 ? '…' : '' }}</span>
                  </li>
                </ul>
              </div>
            </div>
          </article>
        </div>
      </template>

      <!-- 列表视图 -->
      <template v-else>
        <div class="filter-row">
          <button
            v-for="s in (['', 'active', 'done', 'archived'] as const)"
            :key="s"
            type="button"
            class="filter-chip"
            :class="{ active: matterStatusFilter === s }"
            @click="handleSwitchStatus(s)"
          >
            {{ s === '' ? '全部' : statusLabel(s) }}
          </button>
        </div>

        <div v-if="isLoadingMatters" class="empty-hint">加载中...</div>
        <div v-else-if="matters.length === 0" class="empty-hint">
          还没有主题档案。当你持续讨论某个话题(旅行、项目、计划),档案会自动生长出来。
        </div>

        <button
          v-for="m in matters"
          :key="m.id"
          type="button"
          class="matter-card"
          @click="openMatter(m.id)"
        >
          <div class="matter-card-head">
            <span class="matter-card-title">{{ m.title }}</span>
            <span class="status-chip" :class="m.status">{{ statusLabel(m.status) }}</span>
            <span class="matter-count">{{ m.memory_count }} 条记录</span>
          </div>
          <p class="matter-card-desc">{{ m.state_desc || '(暂无状态描述)' }}</p>
        </button>
      </template>
    </div>
  </DialogShell>
</template>

<style scoped>
.safety-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  margin-bottom: 12px;
  border-radius: 8px;
  background: var(--color-surface-raised, #f5f5f5);
}
.safety-text {
  font-size: 12px;
  color: var(--color-text-secondary, #666);
}
.journal-tabs {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}
.journal-tab {
  padding: 6px 14px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: transparent;
  font-size: 13px;
  color: var(--color-text-secondary, #666);
  cursor: pointer;
}
.journal-tab.active {
  background: var(--color-surface-raised, #f0f0f0);
  color: var(--color-text-primary, #111);
}
.tab-count {
  margin-left: 4px;
  font-size: 11px;
  color: var(--color-text-tertiary, #999);
}
.tab-body {
  max-height: 60vh;
  overflow-y: auto;
}
.filter-row {
  display: flex;
  gap: 6px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}
.filter-chip {
  padding: 4px 10px;
  border: none;
  border-radius: 999px;
  background: var(--color-surface-raised, #f0f0f0);
  font-size: 12px;
  color: var(--color-text-secondary, #666);
  cursor: pointer;
}
.filter-chip.active {
  background: var(--color-accent, #333);
  color: #fff;
}
.month-group {
  margin-bottom: 16px;
}
.month-label {
  margin: 8px 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text-tertiary, #999);
}
.journal-card {
  padding: 8px 10px;
  margin-bottom: 6px;
  border: 1px solid var(--color-border, #e5e5e5);
  border-radius: 10px;
  background: var(--color-surface, #fff);
}
.card-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  cursor: pointer;
}
.card-date {
  flex-shrink: 0;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-tertiary, #999);
}
.card-kind {
  flex-shrink: 0;
  padding: 1px 6px;
  border-radius: 4px;
  font-size: 11px;
}
.card-kind.fact {
  background: rgba(59, 130, 246, 0.1);
  color: #3b82f6;
}
.card-kind.loop {
  background: rgba(245, 158, 11, 0.12);
  color: #d97706;
}
.card-pin {
  flex-shrink: 0;
  font-size: 11px;
  color: #f59e0b;
}
.card-content {
  font-size: 13px;
  color: var(--color-text-primary, #222);
  word-break: break-word;
}
.card-sub {
  margin-top: 4px;
  font-size: 11px;
  color: var(--color-text-tertiary, #aaa);
}
.card-expand {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px dashed var(--color-border, #eee);
}
.expand-row {
  margin-bottom: 6px;
}
.expand-label {
  display: inline-block;
  margin-right: 8px;
  font-size: 12px;
  color: var(--color-text-tertiary, #999);
}
.expand-muted {
  font-size: 12px;
  color: var(--color-text-tertiary, #bbb);
}
.link-list {
  margin: 4px 0 0;
  padding-left: 0;
  list-style: none;
}
.link-item {
  margin-bottom: 4px;
  font-size: 12px;
}
.link-time {
  margin-right: 6px;
  color: var(--color-text-tertiary, #999);
  font-variant-numeric: tabular-nums;
}
.link-snippet {
  color: var(--color-text-secondary, #555);
}
.expand-actions {
  display: flex;
  gap: 6px;
  margin-top: 6px;
}
.mini-btn {
  padding: 3px 10px;
  border: 1px solid var(--color-border, #ddd);
  border-radius: 6px;
  background: transparent;
  font-size: 12px;
  color: var(--color-text-secondary, #555);
  cursor: pointer;
}
.mini-btn.primary {
  background: var(--color-accent, #333);
  border-color: var(--color-accent, #333);
  color: #fff;
}
.mini-btn.danger {
  color: #dc2626;
  border-color: rgba(220, 38, 38, 0.35);
}
.mini-btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.edit-area {
  margin-top: 8px;
}
.edit-textarea {
  width: 100%;
  padding: 8px;
  border: 1px solid var(--color-border, #ddd);
  border-radius: 8px;
  font-size: 13px;
  resize: vertical;
}
.edit-actions {
  display: flex;
  gap: 6px;
  margin-top: 6px;
}
.load-more {
  display: block;
  width: 100%;
  padding: 8px;
  border: 1px dashed var(--color-border, #ddd);
  border-radius: 8px;
  background: transparent;
  font-size: 12px;
  color: var(--color-text-secondary, #666);
  cursor: pointer;
}
.load-more:disabled {
  opacity: 0.6;
}
.empty-hint {
  padding: 32px 16px;
  text-align: center;
  font-size: 13px;
  color: var(--color-text-tertiary, #999);
}
.back-btn {
  margin-bottom: 10px;
  padding: 4px 8px;
  border: none;
  background: transparent;
  font-size: 13px;
  color: var(--color-text-secondary, #666);
  cursor: pointer;
}
.matter-card {
  display: block;
  width: 100%;
  padding: 10px 12px;
  margin-bottom: 8px;
  border: 1px solid var(--color-border, #e5e5e5);
  border-radius: 10px;
  background: var(--color-surface, #fff);
  text-align: left;
  cursor: pointer;
}
.matter-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.matter-card-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text-primary, #222);
}
.status-chip {
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 11px;
}
.status-chip.active {
  background: rgba(34, 197, 94, 0.12);
  color: #16a34a;
}
.status-chip.done {
  background: rgba(59, 130, 246, 0.1);
  color: #3b82f6;
}
.status-chip.archived {
  background: rgba(156, 163, 175, 0.15);
  color: #6b7280;
}
.matter-count {
  margin-left: auto;
  font-size: 11px;
  color: var(--color-text-tertiary, #999);
}
.matter-card-desc {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--color-text-secondary, #666);
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.matter-detail {
  margin-top: 4px;
}
.matter-title {
  margin: 0 0 6px;
  font-size: 16px;
  color: var(--color-text-primary, #222);
}
.matter-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.desc-section,
.status-section {
  padding: 10px;
  margin-bottom: 10px;
  border: 1px solid var(--color-border, #eee);
  border-radius: 10px;
}
.desc-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}
.desc-text {
  margin: 0;
  font-size: 13px;
  color: var(--color-text-primary, #333);
  white-space: pre-wrap;
  word-break: break-word;
}
.sync-btn {
  margin-left: auto;
  padding: 4px 12px;
  border: 1px solid var(--color-border, #ddd);
  border-radius: 6px;
  background: transparent;
  font-size: 12px;
  color: var(--color-text-secondary, #555);
  cursor: pointer;
}
.sync-btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.doc-link-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  font-size: 12px;
}
.doc-link {
  color: #3b82f6;
  text-decoration: none;
}
.doc-synced {
  color: var(--color-text-tertiary, #999);
}
/* 28号 N3:目标日徽标;剩 ≤3 天或已过期转警示色 */
.target-chip {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  font-size: 11px;
  font-weight: 500;
  color: var(--accent);
  background: rgba(65, 118, 230, 0.1);
  border-radius: 9999px;
  user-select: none;
}
.target-chip.target-urgent {
  color: #d97706;
  background: rgba(217, 119, 6, 0.12);
}
.timeline-label {
  margin: 14px 0 8px;
  font-size: 12px;
  color: var(--color-text-tertiary, #999);
}
</style>
