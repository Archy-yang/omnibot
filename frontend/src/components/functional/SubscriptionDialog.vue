<script setup lang="ts">
/**
 * SubscriptionDialog — 订阅源管理弹窗(14-订阅源管理技术方案)
 *
 * 对话入口之外的页面管理入口:贴网址即订阅(后端自动发现 RSS 地址),
 * 清单管理(暂停/恢复/退订)。复用 DialogShell(dsh 弹窗壳)。
 * 对话入口(manage_subscriptions 工具)与本页读写同一 subscriptionService。
 */
import { ref, watch } from 'vue';
import DialogShell from '@/components/layout/DialogShell.vue';
import { subscriptionService } from '@/services/subscription';
import { useToast } from '@/composables/useToast';
import type { SubscriptionItem, SubscriptionFeedCandidate } from '@/types/api';

const props = defineProps<{
  visible: boolean;
}>();

const emit = defineEmits<{
  close: [];
}>();

const { error, success } = useToast();

const subscriptions = ref<SubscriptionItem[]>([]);
const inputUrl = ref('');
const isSubmitting = ref(false);
const candidates = ref<SubscriptionFeedCandidate[]>([]);

const load = async () => {
  try {
    const res = await subscriptionService.list();
    subscriptions.value = res.subscriptions;
  } catch (err) {
    error(err instanceof Error ? err.message : '加载订阅失败');
  }
};

watch(
  () => props.visible,
  (v) => {
    if (v) load();
  }
);

const handleAdd = async () => {
  const url = inputUrl.value.trim();
  if (!url || isSubmitting.value) return;
  isSubmitting.value = true;
  try {
    const res = await subscriptionService.add(url);
    if (res.candidates && res.candidates.length > 0) {
      // 多候选:该站点有多个 feed,让用户挑
      candidates.value = res.candidates;
      return;
    }
    if (res.subscription) {
      success(`已订阅「${res.subscription.title}」`);
      inputUrl.value = '';
      await load();
    }
  } catch (err) {
    error(err instanceof Error ? err.message : '订阅失败');
  } finally {
    isSubmitting.value = false;
  }
};

const handlePickCandidate = async (feedUrl: string) => {
  try {
    const res = await subscriptionService.addCandidate(feedUrl);
    if (res.subscription) {
      success(`已订阅「${res.subscription.title}」`);
      candidates.value = [];
      inputUrl.value = '';
      await load();
    }
  } catch (err) {
    error(err instanceof Error ? err.message : '订阅失败');
  }
};

const dismissCandidates = () => {
  candidates.value = [];
};

const handleToggleStatus = async (item: SubscriptionItem) => {
  const next = item.status === 'active' ? 'paused' : 'active';
  try {
    await subscriptionService.setStatus(item.id, next);
    item.status = next;
  } catch (err) {
    error(err instanceof Error ? err.message : '操作失败');
  }
};

const handleRemove = async (item: SubscriptionItem) => {
  try {
    await subscriptionService.remove(item.id);
    subscriptions.value = subscriptions.value.filter((s) => s.id !== item.id);
    success('已退订');
  } catch (err) {
    error(err instanceof Error ? err.message : '退订失败');
  }
};
</script>

<template>
  <DialogShell :visible="visible" title="订阅" width="640px" @close="emit('close')">
    <!-- 订阅输入:贴网址即可,自动发现 RSS 地址 -->
    <div class="sub-add-bar">
      <input
        v-model="inputUrl"
        type="text"
        class="sub-add-input"
        placeholder="输入网站或博客地址，自动找到它的 RSS"
        @keydown.enter="handleAdd"
      />
      <button type="button" class="sub-add-btn" :disabled="isSubmitting || !inputUrl.trim()" @click="handleAdd">
        {{ isSubmitting ? '发现中…' : '订阅' }}
      </button>
    </div>

    <!-- 多候选选择:该站点发现多个 feed,让用户挑 -->
    <div v-if="candidates.length > 0" class="sub-candidates">
      <div class="sub-candidates-title">该站点有 {{ candidates.length }} 个订阅源，选择一个：</div>
      <button
        v-for="c in candidates"
        :key="c.feed_url"
        type="button"
        class="sub-candidate"
        @click="handlePickCandidate(c.feed_url)"
      >
        <span class="sub-candidate-name">{{ c.title || '未命名源' }}</span>
        <span class="sub-candidate-url">{{ c.feed_url }}</span>
      </button>
      <button type="button" class="sub-candidates-dismiss" @click="dismissCandidates">取消</button>
    </div>

    <!-- 订阅清单 -->
    <div v-if="subscriptions.length === 0 && candidates.length === 0" class="sub-empty">
      <p class="sub-empty-title">还没有订阅</p>
      <p class="sub-empty-hint">在上面贴一个网址即可订阅；也可以直接在对话里跟我说「订阅某某的博客」。</p>
    </div>

    <div v-else class="sub-list">
      <div v-for="item in subscriptions" :key="item.id" class="sub-item" :class="{ 'is-paused': item.status === 'paused' }">
        <div class="sub-item-main">
          <div class="sub-item-title">
            {{ item.title }}
            <span v-if="item.status === 'paused'" class="sub-item-paused-badge">已暂停</span>
          </div>
          <div v-if="item.topic_desc" class="sub-item-desc">{{ item.topic_desc }}</div>
          <div class="sub-item-url">{{ item.feed_url }}</div>
        </div>
        <div class="sub-item-actions">
          <button
            type="button"
            class="sub-action-btn"
            :title="item.status === 'active' ? '暂停' : '恢复'"
            @click="handleToggleStatus(item)"
          >
            <svg v-if="item.status === 'active'" width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <rect x="6" y="4" width="4" height="16" rx="1"/>
              <rect x="14" y="4" width="4" height="16" rx="1"/>
            </svg>
            <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
              <path d="M8 5v14l11-7z"/>
            </svg>
          </button>
          <button type="button" class="sub-action-btn delete-btn" title="退订" @click="handleRemove(item)">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="3 6 5 6 21 6"/>
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
            </svg>
          </button>
        </div>
      </div>
    </div>
  </DialogShell>
</template>

<style scoped>
/* 订阅输入条 */
.sub-add-bar {
  display: flex;
  gap: 8px;
  margin-bottom: 16px;
}

.sub-add-input {
  flex: 1;
  min-width: 0;
  height: 36px;
  padding: 0 12px;
  border: 0.5px solid var(--border-l4);
  border-radius: 8px;
  background: var(--input-bg);
  color: var(--label-primary);
  font-size: 14px;
  font-family: inherit;
  outline: none;
  transition: border-color 150ms ease;
}

.sub-add-input:focus {
  border-color: var(--accent);
}

.sub-add-input::placeholder {
  color: var(--label-caption);
}

.sub-add-btn {
  flex: none;
  height: 36px;
  padding: 0 16px;
  border: none;
  border-radius: 18px;
  background: var(--btn-primary-fill);
  color: var(--btn-primary-foreground);
  font-size: 14px;
  font-weight: 500;
  font-family: inherit;
  cursor: pointer;
  transition: background 150ms ease;
}

.sub-add-btn:hover:not(:disabled) {
  background: var(--btn-primary-hover);
}

.sub-add-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* 多候选面板 */
.sub-candidates {
  margin-bottom: 16px;
  padding: 12px;
  border: 0.5px solid var(--border-l2);
  border-radius: 12px;
  background: var(--bg-tip);
}

.sub-candidates-title {
  font-size: 13px;
  color: var(--label-secondary);
  margin-bottom: 8px;
}

.sub-candidate {
  display: block;
  width: 100%;
  text-align: left;
  padding: 8px 10px;
  margin-bottom: 4px;
  border: none;
  border-radius: 8px;
  background: transparent;
  cursor: pointer;
  transition: background 150ms ease;
}

.sub-candidate:hover {
  background: var(--bg-hover);
}

.sub-candidate-name {
  display: block;
  font-size: 14px;
  font-weight: 500;
  color: var(--label-primary);
}

.sub-candidate-url {
  display: block;
  font-size: 12px;
  color: var(--label-tertiary);
  word-break: break-all;
}

.sub-candidates-dismiss {
  border: none;
  background: transparent;
  color: var(--label-tertiary);
  font-size: 12px;
  font-family: inherit;
  cursor: pointer;
  padding: 4px 0;
}

.sub-candidates-dismiss:hover {
  color: var(--label-secondary);
}

/* 空状态 */
.sub-empty {
  padding: 32px 0;
  text-align: center;
}

.sub-empty-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--label-primary);
  margin-bottom: 6px;
}

.sub-empty-hint {
  font-size: 13px;
  color: var(--label-tertiary);
  line-height: 1.6;
}

/* 订阅清单 */
.sub-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.sub-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 12px;
  border: 0.5px solid var(--border-l2);
  border-radius: 12px;
  transition: opacity 150ms ease;
}

.sub-item.is-paused {
  opacity: 0.55;
}

.sub-item-main {
  flex: 1;
  min-width: 0;
}

.sub-item-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--label-primary);
  line-height: 22px;
}

.sub-item-paused-badge {
  margin-left: 6px;
  padding: 1px 6px;
  font-size: 11px;
  font-weight: 400;
  color: var(--label-tertiary);
  background: var(--bg-active);
  border-radius: 4px;
  vertical-align: middle;
}

.sub-item-desc {
  font-size: 13px;
  color: var(--label-tertiary);
  line-height: 20px;
  margin-top: 2px;
}

.sub-item-url {
  font-size: 12px;
  color: var(--label-caption);
  line-height: 18px;
  margin-top: 2px;
  word-break: break-all;
}

.sub-item-actions {
  flex: none;
  display: flex;
  gap: 4px;
}

.sub-action-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--label-tertiary);
  cursor: pointer;
  transition: background 150ms ease, color 150ms ease;
}

.sub-action-btn:hover {
  background: var(--bg-hover);
  color: var(--label-secondary);
}

.sub-action-btn.delete-btn:hover {
  background: var(--error-bg);
  color: var(--error);
}
</style>
