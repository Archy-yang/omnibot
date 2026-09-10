<script setup lang="ts">
/**
 * SkillDrawer — 技能抽屉(13-插件系统 M3)
 *
 * 两个 section:
 *   1. MCP 服务:在线增删改查外部能力服务(地址+密钥),保存即同步发现工具;
 *      手动「同步」可重新拉取工具列表。密钥不明文回显(只显示是否已配置)。
 *   2. 技能:助手当前具备的全部技能(内置/外部接入),逐个启停,即时生效。
 *      (自 SettingsDrawer 迁入,设置抽屉不再展示。)
 */
import { ref, watch } from 'vue';
import { useToast } from '@/composables/useToast';
import type { MCPServerItem, SkillItem } from '@/types/api';
import { skillService } from '@/services/skill';
import { mcpServerService } from '@/services/mcpServer';
import DrawerShell from '@/components/layout/DrawerShell.vue';

const props = defineProps<{
  visible: boolean;
}>();

const emit = defineEmits<{
  close: [];
}>();

const { success, error } = useToast();

// ===== 技能清单 =====
const skills = ref<SkillItem[]>([]);
const skillsLoading = ref(false);
const togglingSkills = ref<Set<string>>(new Set());

const loadSkills = async () => {
  skillsLoading.value = true;
  try {
    const data = await skillService.listSkills();
    skills.value = data.skills;
  } catch (err) {
    console.error('Failed to load skills:', err);
  } finally {
    skillsLoading.value = false;
  }
};

const handleSkillToggle = async (skill: SkillItem, event: Event) => {
  const enabled = (event.target as HTMLInputElement).checked;
  if (togglingSkills.value.has(skill.name)) {
    (event.target as HTMLInputElement).checked = !enabled;
    return;
  }
  togglingSkills.value.add(skill.name);
  try {
    await skillService.updateSkill(skill.name, enabled);
    skill.enabled = enabled;
    success(enabled ? `已启用「${skill.display_name || skill.name}」` : `已停用「${skill.display_name || skill.name}」`);
  } catch (err) {
    (event.target as HTMLInputElement).checked = !enabled;
    error(err instanceof Error ? err.message : '更新技能状态失败');
  } finally {
    togglingSkills.value.delete(skill.name);
  }
};

const skillSourceLabel = (source: string): string =>
  source === 'mcp' ? '外部接入' : '内置';

// ===== MCP 服务管理 =====
const servers = ref<MCPServerItem[]>([]);
const serversLoading = ref(false);
const busyServerId = ref<number | null>(null);

// 表单状态(editingId=null 表示新增)
const showServerForm = ref(false);
const editingId = ref<number | null>(null);
const formName = ref('');
const formBaseUrl = ref('');
const formApiKey = ref(''); // 编辑时留空 = 保留原 key
const formAuthType = ref<'none' | 'bearer' | 'oauth'>('bearer');
const formClientId = ref('');
const formClientSecret = ref(''); // 编辑时留空 = 保留原 secret
const formScopes = ref('');
const formEnabled = ref(true);
const formSaving = ref(false);

const loadServers = async () => {
  serversLoading.value = true;
  try {
    const data = await mcpServerService.listServers();
    servers.value = data.servers;
  } catch (err) {
    console.error('Failed to load MCP servers:', err);
  } finally {
    serversLoading.value = false;
  }
};

const resetForm = () => {
  editingId.value = null;
  formName.value = '';
  formBaseUrl.value = '';
  formApiKey.value = '';
  formAuthType.value = 'bearer';
  formClientId.value = '';
  formClientSecret.value = '';
  formScopes.value = '';
  formEnabled.value = true;
  showServerForm.value = false;
};

const openCreateForm = () => {
  resetForm();
  showServerForm.value = true;
};

const openEditForm = (server: MCPServerItem) => {
  editingId.value = server.id;
  formName.value = server.name;
  formBaseUrl.value = server.base_url;
  formApiKey.value = ''; // 留空 = 保留原 key
  formAuthType.value = (server.auth_type as 'none' | 'bearer' | 'oauth') || 'bearer';
  formClientId.value = ''; // 后端留空 = 保留原值
  formClientSecret.value = '';
  formScopes.value = '';
  formEnabled.value = server.enabled;
  showServerForm.value = true;
};

const validateForm = (): string => {
  if (!formName.value.trim()) return '请填写服务名称';
  if (!formBaseUrl.value.trim()) return '请填写服务地址';
  if (!/^https?:\/\//.test(formBaseUrl.value.trim())) return '服务地址必须是 http(s) 链接';
  return '';
};

const handleSaveServer = async () => {
  const invalid = validateForm();
  if (invalid) {
    error(invalid);
    return;
  }
  formSaving.value = true;
  const body = {
    name: formName.value.trim(),
    base_url: formBaseUrl.value.trim(),
    api_key: formApiKey.value, // 空 = 保留原值(编辑时)
    auth_type: formAuthType.value,
    oauth_client_id: formClientId.value,
    oauth_client_secret: formClientSecret.value,
    oauth_scopes: formScopes.value,
    enabled: formEnabled.value,
  };
  try {
    if (editingId.value === null) {
      await mcpServerService.createServer(body);
      success('服务已保存并同步,发现的新技能默认关闭,请到下方逐个开启');
    } else {
      await mcpServerService.updateServer(editingId.value, body);
      success('服务已更新并重新同步');
    }
    resetForm();
    await Promise.all([loadServers(), loadSkills()]);
  } catch (err) {
    error(err instanceof Error ? err.message : '保存失败,请检查地址与密钥');
  } finally {
    formSaving.value = false;
  }
};

const handleDeleteServer = async (server: MCPServerItem) => {
  if (!window.confirm(`确定删除服务「${server.name}」?它带来的 ${server.tool_count < 0 ? 0 : server.tool_count} 个外部技能将一并移除。`)) {
    return;
  }
  busyServerId.value = server.id;
  try {
    await mcpServerService.deleteServer(server.id);
    success(`已删除服务「${server.name}」`);
    await Promise.all([loadServers(), loadSkills()]);
  } catch (err) {
    error(err instanceof Error ? err.message : '删除失败');
  } finally {
    busyServerId.value = null;
  }
};

const handleSyncServer = async (server: MCPServerItem) => {
  busyServerId.value = server.id;
  try {
    const res = await mcpServerService.syncServer(server.id);
    if (res.err) {
      error(`同步失败:${res.err}`);
    } else {
      success(`同步完成,发现 ${res.tool_count} 个工具`);
    }
    await Promise.all([loadServers(), loadSkills()]);
  } catch (err) {
    error(err instanceof Error ? err.message : '同步失败');
  } finally {
    busyServerId.value = null;
  }
};

const handleAuthorizeServer = async (server: MCPServerItem) => {
  busyServerId.value = server.id;
  try {
    const res = await mcpServerService.authorizeServer(server.id);
    window.open(res.authorization_url, '_blank');
    success('已打开服务商授权页,完成后回到本页点「同步」');
  } catch (err) {
    error(err instanceof Error ? err.message : '发起授权失败');
  } finally {
    busyServerId.value = null;
  }
};

const toolCountText = (server: MCPServerItem): string => {
  if (server.tool_count < 0) return '未同步';
  return `${server.tool_count} 个工具`;
};

// 抽屉打开时拉清单
watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      loadSkills();
      loadServers();
    }
  }
);
</script>

<template>
  <DrawerShell :visible="visible" title="技能" @close="emit('close')">
    <!-- ===== MCP 服务 section ===== -->
    <div class="section-title">外部能力服务</div>
    <p class="section-hint">接入 MCP 服务后,它提供的技能会出现在下方清单中(默认关闭)。密钥加密保存,不会明文显示。</p>

    <div v-if="serversLoading" class="hint-text">加载中...</div>
    <div v-else-if="servers.length === 0 && !showServerForm" class="hint-text">
      还没有接入外部服务
    </div>
    <ul v-else class="server-list">
      <li v-for="server in servers" :key="server.id" class="server-item">
        <div class="server-info">
          <div class="server-name-row">
            <span class="server-name">{{ server.name }}</span>
            <span v-if="server.auth_type === 'oauth'" class="server-badge" :class="server.authorized ? '' : 'is-off'">
              {{ server.authorized ? '已授权' : '待授权' }}
            </span>
            <span class="server-badge" :class="{ 'is-off': !server.enabled }">
              {{ server.enabled ? '已启用' : '已停用' }}
            </span>
            <span class="server-count">{{ toolCountText(server) }}</span>
          </div>
          <div class="server-url">{{ server.base_url }}</div>
        </div>
        <div class="server-actions">
          <button
            v-if="server.auth_type === 'oauth' && !server.authorized"
            type="button"
            class="server-btn is-primary"
            :disabled="busyServerId === server.id"
            @click="handleAuthorizeServer(server)"
          >授权</button>
          <button
            type="button"
            class="server-btn"
            :disabled="busyServerId === server.id"
            @click="handleSyncServer(server)"
          >同步</button>
          <button
            type="button"
            class="server-btn"
            :disabled="busyServerId === server.id"
            @click="openEditForm(server)"
          >编辑</button>
          <button
            type="button"
            class="server-btn is-danger"
            :disabled="busyServerId === server.id"
            @click="handleDeleteServer(server)"
          >删除</button>
        </div>
      </li>
    </ul>

    <!-- 新增/编辑表单 -->
    <div v-if="showServerForm" class="server-form">
      <div class="form-field">
        <label class="form-label" for="mcp-name">服务名称</label>
        <input
          id="mcp-name"
          v-model="formName"
          class="form-input"
          type="text"
          placeholder="如 github"
        />
      </div>
      <div class="form-field">
        <label class="form-label" for="mcp-url">服务地址</label>
        <input
          id="mcp-url"
          v-model="formBaseUrl"
          class="form-input"
          type="url"
          placeholder="https://mcp.example.com/mcp"
        />
      </div>
      <div class="form-field">
        <label class="form-label" for="mcp-auth-type">鉴权方式</label>
        <select id="mcp-auth-type" v-model="formAuthType" class="form-input">
          <option value="bearer">API Key（静态令牌）</option>
          <option value="oauth">OAuth 2.1（远程托管服务标准）</option>
          <option value="none">无鉴权</option>
        </select>
      </div>
      <div v-if="formAuthType === 'bearer'" class="form-field">
        <label class="form-label" for="mcp-key">访问密钥</label>
        <input
          id="mcp-key"
          v-model="formApiKey"
          class="form-input"
          type="password"
          :placeholder="editingId === null ? '无鉴权服务可留空' : '留空则保留原密钥'"
          autocomplete="new-password"
        />
      </div>
      <template v-if="formAuthType === 'oauth'">
        <p class="section-hint">保存后点服务行的「授权」完成跳转授权;Client ID 留空时将尝试自动注册。</p>
        <div class="form-field">
          <label class="form-label" for="mcp-client-id">Client ID（可留空自动注册）</label>
          <input
            id="mcp-client-id"
            v-model="formClientId"
            class="form-input"
            type="text"
            :placeholder="editingId === null ? '留空尝试动态注册' : '留空则保留原值'"
          />
        </div>
        <div class="form-field">
          <label class="form-label" for="mcp-client-secret">Client Secret（可留空）</label>
          <input
            id="mcp-client-secret"
            v-model="formClientSecret"
            class="form-input"
            type="password"
            :placeholder="editingId === null ? '公共客户端可留空' : '留空则保留原值'"
            autocomplete="new-password"
          />
        </div>
        <div class="form-field">
          <label class="form-label" for="mcp-scopes">Scopes（逗号分隔,可留空）</label>
          <input
            id="mcp-scopes"
            v-model="formScopes"
            class="form-input"
            type="text"
            placeholder="如 repo,read:user"
          />
        </div>
      </template>
      <label class="server-enable-row">
        <input v-model="formEnabled" type="checkbox" />
        <span>保存后立即连接并同步工具</span>
      </label>
      <div class="form-actions">
        <button type="button" class="form-btn" @click="resetForm">取消</button>
        <button type="button" class="form-btn is-primary" :disabled="formSaving" @click="handleSaveServer">
          {{ formSaving ? '保存中...' : '保存' }}
        </button>
      </div>
    </div>
    <button
      v-else
      type="button"
      class="add-server-btn"
      @click="openCreateForm"
    >+ 接入新服务</button>

    <!-- ===== 技能清单 section ===== -->
    <div class="section-skills">
      <div class="section-title">技能</div>

      <div v-if="skillsLoading" class="hint-text">加载中...</div>
      <div v-else-if="skills.length === 0" class="hint-text">暂无可用技能</div>
      <ul v-else class="skill-list">
        <li
          v-for="skill in skills"
          :key="skill.name"
          class="skill-item"
          :class="{ 'is-disabled': !skill.available }"
        >
          <div class="skill-info">
            <div class="skill-name-row">
              <span class="skill-name">{{ skill.display_name || skill.name }}</span>
              <span class="skill-source-badge" :class="`is-${skill.source}`">
                {{ skillSourceLabel(skill.source) }}
              </span>
            </div>
            <div class="skill-desc">{{ skill.description }}</div>
          </div>
          <label class="skill-switch" :title="skill.available ? '' : '该技能暂不可用'">
            <input
              type="checkbox"
              role="switch"
              :aria-label="`启用${skill.display_name || skill.name}`"
              :checked="skill.enabled"
              :disabled="!skill.available || togglingSkills.has(skill.name)"
              @change="handleSkillToggle(skill, $event)"
            />
            <span class="skill-switch-slider"></span>
          </label>
        </li>
      </ul>
    </div>

    <div class="drawer-footer-spacer"></div>
  </DrawerShell>
</template>

<style scoped>
.section-title {
  font-size: 15px;
  font-weight: 600;
  color: #171717;
  margin-bottom: 8px;
}
.section-hint {
  font-size: 12px;
  color: #9ca3af;
  margin: 0 0 12px;
  line-height: 1.5;
}
.hint-text {
  font-size: 13px;
  color: #9ca3af;
  padding: 8px 0;
}

/* ===== MCP 服务列表 ===== */
.server-list {
  list-style: none;
  margin: 0 0 12px;
  padding: 0;
}
.server-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 0;
  border-bottom: 1px solid #f5f5f5;
}
.server-item:last-child {
  border-bottom: none;
}
.server-info {
  flex: 1;
  min-width: 0;
}
.server-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.server-name {
  font-size: 14px;
  font-weight: 500;
  color: #171717;
}
.server-badge {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  background: #f0fdf4;
  color: #15803d;
  white-space: nowrap;
}
.server-badge.is-off {
  background: #f3f4f6;
  color: #9ca3af;
}
.server-count {
  font-size: 11px;
  color: #9ca3af;
  white-space: nowrap;
}
.server-url {
  font-size: 12px;
  color: #6b7280;
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.server-actions {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}
.server-btn {
  font-size: 12px;
  padding: 4px 8px;
  border-radius: 6px;
  border: 1px solid #e5e7eb;
  background: #ffffff;
  color: #374151;
  cursor: pointer;
  font-family: inherit;
}
.server-btn:hover {
  border-color: #d1d5db;
  background: #f9fafb;
}
.server-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.server-btn.is-primary {
  color: #10a37f;
  border-color: #a7f3d0;
}
.server-btn.is-primary:hover {
  background: #f0fdf4;
}
.server-btn.is-danger {
  color: #dc2626;
  border-color: #fecaca;
}
.server-btn.is-danger:hover {
  background: #fef2f2;
}

/* ===== 表单 ===== */
.server-form {
  margin: 12px 0;
  padding: 12px;
  border: 1px solid #e5e7eb;
  border-radius: 10px;
  background: #f9fafb;
}
.form-field {
  margin-bottom: 10px;
}
.form-label {
  display: block;
  font-size: 13px;
  color: #374151;
  margin-bottom: 4px;
}
.form-input {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid #d1d5db;
  border-radius: 8px;
  font-size: 14px;
  font-family: inherit;
  box-sizing: border-box;
}
.form-input:focus {
  outline: none;
  border-color: #10a37f;
}
.server-enable-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: #374151;
  cursor: pointer;
}
.form-actions {
  margin-top: 12px;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.form-btn {
  padding: 7px 14px;
  border-radius: 8px;
  font-size: 13px;
  border: 1px solid #d1d5db;
  background: #ffffff;
  color: #374151;
  cursor: pointer;
  font-family: inherit;
}
.form-btn.is-primary {
  background: #10a37f;
  border-color: #10a37f;
  color: #ffffff;
}
.form-btn.is-primary:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
.add-server-btn {
  width: 100%;
  padding: 9px;
  border: 1px dashed #d1d5db;
  border-radius: 10px;
  background: transparent;
  color: #6b7280;
  font-size: 13px;
  cursor: pointer;
  font-family: inherit;
  margin-bottom: 8px;
}
.add-server-btn:hover {
  border-color: #10a37f;
  color: #10a37f;
}

/* ===== 技能清单 ===== */
.section-skills {
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid #f0f0f0;
}
.skill-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.skill-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 0;
  border-bottom: 1px solid #f5f5f5;
}
.skill-item:last-child {
  border-bottom: none;
}
.skill-item.is-disabled .skill-name,
.skill-item.is-disabled .skill-desc {
  color: #9ca3af;
}
.skill-info {
  flex: 1;
  min-width: 0;
}
.skill-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.skill-name {
  font-size: 14px;
  font-weight: 500;
  color: #171717;
}
.skill-source-badge {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  background: #f3f4f6;
  color: #6b7280;
  white-space: nowrap;
}
.skill-source-badge.is-mcp {
  background: #eff6ff;
  color: #1d4ed8;
}
.skill-desc {
  font-size: 12px;
  color: #6b7280;
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

/* 开关(checkbox + slider) */
.skill-switch {
  position: relative;
  flex-shrink: 0;
  width: 36px;
  height: 20px;
  cursor: pointer;
}
.skill-switch input {
  position: absolute;
  opacity: 0;
  width: 100%;
  height: 100%;
  margin: 0;
  cursor: pointer;
}
.skill-switch input:disabled {
  cursor: not-allowed;
}
.skill-switch-slider {
  position: absolute;
  inset: 0;
  border-radius: 10px;
  background: #d1d5db;
  transition: background 150ms ease;
  pointer-events: none;
}
.skill-switch-slider::before {
  content: '';
  position: absolute;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #ffffff;
  top: 2px;
  left: 2px;
  transition: transform 150ms ease;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2);
}
.skill-switch input:checked + .skill-switch-slider {
  background: #10a37f;
}
.skill-switch input:checked + .skill-switch-slider::before {
  transform: translateX(16px);
}
.skill-switch input:disabled + .skill-switch-slider {
  opacity: 0.5;
}

.drawer-footer-spacer {
  height: 32px;
}
</style>
