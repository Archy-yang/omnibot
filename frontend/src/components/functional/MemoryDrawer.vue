<script setup lang="ts">
/**
 * MemoryDrawer — v2.0 记忆管理抽屉
 *
 * 设计稿:docs/60-设计/omnibot-prototype/pages/v2-memory.html。
 * 替代 v1.10 SettingsPanel 的「记忆」Tab 和第一轮误做的 /memory 独立页路由。
 *
 * 业务逻辑沿用原 MemorySection.vue:
 *   - 200 字限制 + 空内容拒绝 + 安全提醒
 *   - 单条编辑(内联)+ 单条删除 + 清空全部(二次确认)
 *   - Toast 反馈
 *
 * 视觉对齐设计稿,不用 NaiveUI(原 NCard/NList/NEmpty/NInput 全部移除)。
 */
import { computed, onMounted, ref, watch } from 'vue';
import DrawerShell from '@/components/layout/DrawerShell.vue';
import { useMemory } from '@/composables/useMemory';
import { useToast } from '@/composables/useToast';

const props = defineProps<{
  visible: boolean;
}>();

const emit = defineEmits<{
  close: [];
}>();

const {
  memories,
  isLoading,
  isCreating,
  isClearing,
  loadMemories,
  createMemory,
  clearMemories,
  deleteMemory,
  updateMemory,
} = useMemory();
const { success, error } = useToast();

// ===== 注入分层双 tab(PRD 修订):手动=我交代的(常驻) / 自动=沉淀管线提取(工具检索) =====
type MemoryTab = 'manual' | 'auto';
const activeTab = ref<MemoryTab>('manual');

// 老数据无 source 字段视为 manual
const isAuto = (m: { source?: string }): boolean => m.source === 'auto';
const manualMemories = computed(() => memories.value.filter((m) => !isAuto(m)));
const autoMemories = computed(() => memories.value.filter((m) => isAuto(m)));
const activeMemories = computed(() =>
  activeTab.value === 'manual' ? manualMemories.value : autoMemories.value
);

const handleSwitchTab = (tab: MemoryTab): void => {
  if (activeTab.value === tab) return;
  activeTab.value = tab;
  editingId.value = null; // 切 tab 退出编辑态
  editingContent.value = '';
};

// 新建态
const memoryInput = ref<string>('');
const trimmedMemory = computed(() => memoryInput.value.trim());
const memoryLength = computed(() => [...trimmedMemory.value].length);
const canSubmit = computed(
  () => !!trimmedMemory.value && memoryLength.value <= 200 && !isCreating.value
);

// 编辑态
const editingId = ref<number | null>(null);
const editingContent = ref<string>('');
const trimmedEditing = computed(() => editingContent.value.trim());
const editingLength = computed(() => [...trimmedEditing.value].length);

// 抽屉打开时加载列表(每次打开都拉,保证跨入口同步后新增的记忆能看到)
watch(
  () => props.visible,
  (v) => {
    if (v) {
      loadMemories().catch((err: unknown) => {
        error(getErrorMessage(err));
      });
    } else {
      // 关闭时清空编辑态,避免下次打开还停留在某条编辑
      editingId.value = null;
      editingContent.value = '';
    }
  }
);

// 首次挂载时如果 visible=true 也加载一次(防止 watch 没触发)
onMounted(() => {
  if (props.visible) {
    loadMemories().catch((err: unknown) => {
      error(getErrorMessage(err));
    });
  }
});

const getErrorMessage = (err: unknown): string => {
  return err instanceof Error ? err.message : '服务暂时不可用,请稍后再试。';
};

const handleCreateMemory = async (): Promise<void> => {
  if (!trimmedMemory.value) {
    error('请输入要长期记住的内容。');
    return;
  }
  if (memoryLength.value > 200) {
    error('这条记忆太长了,请控制在 200 字以内。');
    return;
  }
  try {
    await createMemory(trimmedMemory.value);
    memoryInput.value = '';
    success('已记住。提醒:请不要保存密码、API Key、身份证号等敏感信息。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

const handleClearMemories = async (): Promise<void> => {
  const source = activeTab.value;
  const tip =
    source === 'manual'
      ? '确定要清空全部「我交代的」记忆吗?清空后无法恢复。'
      : '确定要清空全部「自动沉淀的」记忆吗?清空后无法恢复;后续对话中助手仍会继续自动沉淀。';
  if (!window.confirm(tip)) return;
  try {
    await clearMemories(source);
    success(source === 'manual' ? '已清空你手动添加的记忆。' : '已清空全部自动沉淀的记忆。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

const handleDeleteMemory = async (id: number): Promise<void> => {
  if (!window.confirm('确定要删除这条记忆吗?')) return;
  try {
    await deleteMemory(id);
    success('已删除记忆。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

const startEditing = (id: number, content: string): void => {
  editingId.value = id;
  editingContent.value = content;
};

const cancelEditing = (): void => {
  editingId.value = null;
  editingContent.value = '';
};

const handleUpdateMemory = async (id: number): Promise<void> => {
  if (!trimmedEditing.value) {
    error('请输入要长期记住的内容。');
    return;
  }
  if (editingLength.value > 200) {
    error('这条记忆太长了,请控制在 200 字以内。');
    return;
  }
  try {
    await updateMemory(id, trimmedEditing.value);
    editingId.value = null;
    editingContent.value = '';
    success('已更新记忆。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

// 时间格式化:"2026-06-20 10:15"(取自 v2-memory.html 设计稿)
const formatTime = (iso: string): string => {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
};
</script>

<template>
  <DrawerShell :visible="visible" title="记忆" @close="emit('close')">
    <!-- 安全提醒条 -->
    <div class="safety-bar">
      <svg class="safety-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
        <line x1="12" y1="9" x2="12" y2="13"/>
        <line x1="12" y1="17" x2="12.01" y2="17"/>
      </svg>
      <span class="safety-text">请不要保存密码、API Key、身份证号等敏感信息</span>
    </div>

    <!-- 记忆列表区 -->
    <div class="memory-list-section">
      <!-- 双 tab:注入分层(PRD 修订)——tab 栏位置固定,切 tab 不跳 -->
      <div class="memory-tabs">
        <button
          type="button"
          class="memory-tab"
          :class="{ active: activeTab === 'manual' }"
          @click="handleSwitchTab('manual')"
        >
          我交代的
          <span class="tab-count">{{ manualMemories.length }}</span>
        </button>
        <button
          type="button"
          class="memory-tab"
          :class="{ active: activeTab === 'auto' }"
          @click="handleSwitchTab('auto')"
        >
          自动沉淀的
          <span class="tab-count">{{ autoMemories.length }}</span>
        </button>
      </div>

      <!-- 新增记忆区(「我交代的」tab 面板内——新增入口只属于手动记忆) -->
      <div v-if="activeTab === 'manual'" class="memory-input-section">
        <textarea
          v-model="memoryInput"
          class="memory-textarea"
          placeholder="输入希望助手长期记住的偏好、背景或项目说明..."
          :maxlength="220"
          rows="4"
        ></textarea>
        <div class="memory-toolbar">
          <span class="char-count" :class="{ 'is-over': memoryLength > 200 }">
            {{ memoryLength }} / 200
          </span>
          <button
            type="button"
            class="add-btn"
            :disabled="!canSubmit"
            @click="handleCreateMemory"
          >
            {{ isCreating ? '添加中...' : '添加' }}
          </button>
        </div>
      </div>

      <div class="memory-list-header">
        <span class="memory-list-title">
          {{ activeTab === 'manual' ? '你交代的记忆(每次对话我都会记得)' : '助手从对话中自动沉淀的记忆' }}
        </span>
        <div class="memory-list-meta">
          <span class="memory-count">{{ activeMemories.length }} 条</span>
          <button
            v-if="activeMemories.length > 0"
            type="button"
            class="clear-all-btn"
            :disabled="isClearing"
            @click="handleClearMemories"
          >
            清空本类
          </button>
        </div>
      </div>

      <!-- 空状态(两 tab 文案区分) -->
      <div v-if="!isLoading && activeMemories.length === 0" class="memory-empty">
        <template v-if="activeTab === 'manual'">
          <p>你还没有主动交代过任何记忆。</p>
          <p class="memory-empty-hint">
            在上方添加一条希望助手长期记住的偏好、背景或项目说明;也可以在微信/飞书发送
            <code>#记住</code>。
          </p>
        </template>
        <template v-else>
          <p>还没有自动沉淀的记忆。</p>
          <p class="memory-empty-hint">
            和助手聊天时,重要信息会被自动沉淀到这里,你随时可以查看或删除。
          </p>
        </template>
      </div>

      <!-- 加载中 -->
      <div v-else-if="isLoading && memories.length === 0" class="memory-loading">
        加载中...
      </div>

      <!-- 列表 -->
      <div v-else class="memory-list">
        <div
          v-for="(memory, index) in activeMemories"
          :key="memory.id"
          class="memory-card"
          :class="{ editing: editingId === memory.id }"
        >
          <!-- 序号 -->
          <div
            class="memory-index"
            :class="{ 'editing-index': editingId === memory.id }"
          >
            {{ index + 1 }}
          </div>

          <!-- 展示态 -->
          <template v-if="editingId !== memory.id">
            <div class="memory-content">
              <div class="memory-text">{{ memory.content }}</div>
              <div v-if="formatTime(memory.created_at)" class="memory-time">
                {{ formatTime(memory.created_at) }}
              </div>
            </div>
            <div class="memory-actions">
              <button
                type="button"
                class="memory-action-btn edit-btn"
                title="编辑"
                @click="startEditing(memory.id, memory.content)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <button
                type="button"
                class="memory-action-btn delete-btn"
                title="删除"
                @click="handleDeleteMemory(memory.id)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                  <line x1="10" y1="11" x2="10" y2="17"/>
                  <line x1="14" y1="11" x2="14" y2="17"/>
                </svg>
              </button>
            </div>
          </template>

          <!-- 编辑态 -->
          <div v-else class="memory-content editing-content">
            <textarea
              v-model="editingContent"
              class="memory-edit-textarea"
              :maxlength="220"
              rows="3"
            ></textarea>
            <div class="memory-edit-toolbar">
              <span class="char-count" :class="{ 'is-over': editingLength > 200 }">
                {{ editingLength }} / 200
              </span>
              <div class="memory-edit-actions">
                <button type="button" class="edit-cancel-btn" @click="cancelEditing">
                  取消
                </button>
                <button
                  type="button"
                  class="edit-save-btn"
                  :disabled="!trimmedEditing || editingLength > 200"
                  @click="handleUpdateMemory(memory.id)"
                >
                  保存
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </DrawerShell>
</template>

<style scoped>
/* ===== 安全提醒条 ===== */
.safety-bar {
  padding: 10px 12px;
  background: #fffbeb;
  border-left: 3px solid #f59e0b;
  border-radius: 0 8px 8px 0;
  display: flex;
  align-items: center;
  gap: 8px;
}
.safety-icon {
  flex-shrink: 0;
  color: #f59e0b;
}
.safety-text {
  font-size: 13px;
  color: #92400e;
  line-height: 1.4;
}

/* ===== 注入分层双 tab ===== */
.memory-tabs {
  display: flex;
  gap: 8px;
  margin-top: 20px;
}
.memory-tab {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 8px 12px;
  border: 1px solid #e5e5e5;
  border-radius: 8px;
  background: #f8f9fa;
  color: #6b7280;
  font-size: 13px;
  font-family: inherit;
  cursor: pointer;
  transition: all 150ms ease;
}
.memory-tab:hover {
  border-color: #10a37f;
  color: #10a37f;
}
.memory-tab.active {
  background: #10a37f;
  border-color: #10a37f;
  color: #ffffff;
  font-weight: 500;
}
.tab-count {
  min-width: 20px;
  padding: 0 6px;
  border-radius: 10px;
  background: rgba(0, 0, 0, 0.08);
  font-size: 12px;
  text-align: center;
}
.memory-tab.active .tab-count {
  background: rgba(255, 255, 255, 0.25);
}
.memory-empty-hint code {
  padding: 1px 5px;
  background: #e5e7eb;
  border-radius: 4px;
  font-family: 'SF Mono', 'Menlo', monospace;
  font-size: 12px;
  color: #171717;
}

/* ===== 新增记忆区 ===== */
.memory-input-section {
  margin-top: 20px;
}

.memory-textarea {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid #e5e5e5;
  border-radius: 8px;
  font-size: 14px;
  color: #171717;
  font-family: inherit;
  line-height: 1.5;
  min-height: 80px;
  resize: vertical;
  outline: none;
  transition: border-color 150ms ease, box-shadow 150ms ease;
  background: #fff;
}
.memory-textarea::placeholder {
  color: #ccc;
}
.memory-textarea:focus {
  border-color: #10a37f;
  box-shadow: 0 0 0 2px rgba(16, 163, 127, 0.08);
}

.memory-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 8px;
}

.char-count {
  font-size: 12px;
  color: #ccc;
}
.char-count.is-over {
  color: #ef4444;
}

.add-btn {
  padding: 6px 16px;
  background: #10a37f;
  color: #ffffff;
  border: none;
  border-radius: 8px;
  font-size: 14px;
  font-family: inherit;
  cursor: pointer;
  transition: background 150ms ease;
}
.add-btn:hover:not(:disabled) {
  background: #0d8c6d;
}
.add-btn:disabled {
  background: #d4d4d4;
  cursor: not-allowed;
}

/* ===== 记忆列表区 ===== */
.memory-list-section {
  margin-top: 20px;
}

.memory-list-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.memory-list-title {
  font-size: 14px;
  font-weight: 600;
  color: #171717;
}
.memory-list-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}
.memory-count {
  font-size: 12px;
  color: #ccc;
}
.clear-all-btn {
  font-size: 12px;
  color: #ef4444;
  background: none;
  border: none;
  cursor: pointer;
  font-family: inherit;
  transition: text-decoration 150ms ease;
  padding: 0;
}
.clear-all-btn:hover:not(:disabled) {
  text-decoration: underline;
}
.clear-all-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.memory-empty {
  margin-top: 12px;
  padding: 16px;
  text-align: center;
  color: #999;
  font-size: 13px;
  line-height: 1.6;
}
.memory-empty-hint {
  margin-top: 4px;
  font-size: 12px;
  color: #bbb;
}
.memory-loading {
  margin-top: 12px;
  padding: 16px;
  text-align: center;
  font-size: 13px;
  color: #999;
}

.memory-list {
  margin-top: 8px;
}

.memory-card {
  padding: 12px 14px;
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  margin-bottom: 6px;
  display: flex;
  align-items: flex-start;
  gap: 10px;
  transition: background 150ms ease, border-color 150ms ease;
}
.memory-card:hover {
  background: #fafafa;
  border-color: #e5e5e5;
}

.memory-index {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: #f0f0f0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  color: #999;
  flex-shrink: 0;
  margin-top: 1px;
}

.memory-content {
  flex: 1;
  min-width: 0;
}
.memory-text {
  font-size: 14px;
  color: #333;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
.memory-time {
  font-size: 11px;
  color: #ccc;
  margin-top: 4px;
}

.memory-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}
.memory-action-btn {
  width: 24px;
  height: 24px;
  border-radius: 4px;
  border: none;
  background: transparent;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #ccc;
  transition: all 150ms ease;
}
.memory-action-btn.edit-btn:hover {
  color: #999;
  background: #f5f5f5;
}
.memory-action-btn.delete-btn:hover {
  color: #ef4444;
  background: #fef2f2;
}

/* ===== 编辑态 ===== */
.memory-card.editing {
  background: #fafafa;
  border-color: #e5e5e5;
}
.memory-card.editing .editing-index {
  background: #10a37f;
  color: white;
}

.editing-content {
  width: 100%;
}

.memory-edit-textarea {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid #e5e5e5;
  border-radius: 6px;
  font-size: 14px;
  color: #171717;
  font-family: inherit;
  line-height: 1.5;
  min-height: 48px;
  resize: vertical;
  outline: none;
  background: #ffffff;
  transition: border-color 150ms ease, box-shadow 150ms ease;
}
.memory-edit-textarea:focus {
  border-color: #10a37f;
  box-shadow: 0 0 0 2px rgba(16, 163, 127, 0.08);
}

.memory-edit-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 8px;
}
.memory-edit-actions {
  display: flex;
  gap: 6px;
}

.edit-cancel-btn {
  padding: 4px 12px;
  background: #f5f5f5;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-family: inherit;
  color: #666;
  cursor: pointer;
  transition: background 150ms ease;
}
.edit-cancel-btn:hover {
  background: #ebebeb;
}

.edit-save-btn {
  padding: 4px 12px;
  background: #10a37f;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-family: inherit;
  color: white;
  cursor: pointer;
  transition: background 150ms ease;
}
.edit-save-btn:hover:not(:disabled) {
  background: #0d8c6d;
}
.edit-save-btn:disabled {
  background: #b8d9ce;
  cursor: not-allowed;
}
</style>
