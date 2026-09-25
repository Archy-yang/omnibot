<script setup lang="ts">
/**
 * ToolDrawer — 工具抽屉(13-插件系统 M3)
 *
 * 两个 section:
 *   1. MCP 服务:在线增删改查外部能力服务(地址+密钥),保存即同步发现工具;
 *      手动「同步」可重新拉取工具列表。密钥不明文回显(只显示是否已配置)。
 *   2. 工具:助手当前具备的全部工具(内置/外部接入),逐个启停,即时生效。
 *      (自 SettingsDrawer 迁入,设置抽屉不再展示。)
 */
import { ref, watch } from 'vue';
import { useToast } from '@/composables/useToast';
import type { MCPServerItem, ToolItem } from '@/types/api';
import { toolService } from '@/services/tool';
import { mcpServerService } from '@/services/mcpServer';
import DialogShell from '@/components/layout/DialogShell.vue';

const props = defineProps<{
  visible: boolean;
}>();

const emit = defineEmits<{
  close: [];
}>();

const { success, error } = useToast();

// ===== 工具清单 =====
const tools = ref<ToolItem[]>([]);
const toolsLoading = ref(false);
const togglingTools = ref<Set<string>>(new Set());

const loadTools = async () => {
  toolsLoading.value = true;
  try {
    const data = await toolService.listTools();
    tools.value = data.tools;
  } catch (err) {
    console.error('Failed to load tools:', err);
  } finally {
    toolsLoading.value = false;
  }
};

const handleToolToggle = async (tool: ToolItem, event: Event) => {
  const enabled = (event.target as HTMLInputElement).checked;
  if (togglingTools.value.has(tool.name)) {
    (event.target as HTMLInputElement).checked = !enabled;
    return;
  }
  togglingTools.value.add(tool.name);
  try {
    await toolService.updateTool(tool.name, enabled);
    tool.enabled = enabled;
    success(enabled ? `已启用「${tool.display_name || tool.name}」` : `已停用「${tool.display_name || tool.name}」`);
  } catch (err) {
    (event.target as HTMLInputElement).checked = !enabled;
    error(err instanceof Error ? err.message : '更新工具状态失败');
  } finally {
    togglingTools.value.delete(tool.name);
  }
};

// ===== MCP 服务管理 =====
const servers = ref<MCPServerItem[]>([]);
// 抽屉双 tab:工具(内置 function call)/连接器(MCP server 及其工具能力)
const activeTab = ref<'tools' | 'connectors'>('tools');
const expandedServers = ref<Set<number>>(new Set());
const toggleServerExpand = (id: number) => {
  const next = new Set(expandedServers.value);
  if (next.has(id)) next.delete(id); else next.add(id);
  expandedServers.value = next;
};
const serversLoading = ref(false);
const busyServerId = ref<number | null>(null);

// 表单状态(editingId=null 表示新增)
const showServerForm = ref(false);
const editingId = ref<number | null>(null);
const formName = ref('');
const formDescription = ref(''); // 能力概述(选填)
const formBaseUrl = ref('');
const formApiKey = ref(''); // 编辑时留空 = 保留原 key
const formAuthType = ref<'none' | 'bearer' | 'oauth'>('bearer');
// 传输协议:''=自动(streamable 失败回退 SSE)/'streamable'/'sse'
const formTransport = ref<'' | 'streamable' | 'sse'>('');
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
  formDescription.value = '';
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
  formDescription.value = server.description ?? '';
  formBaseUrl.value = server.base_url;
  formApiKey.value = ''; // 留空 = 保留原 key
  formAuthType.value = (server.auth_type as 'none' | 'bearer' | 'oauth') || 'bearer';
  formTransport.value = (server.transport as '' | 'streamable' | 'sse') || '';
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
    description: formDescription.value.trim(),
    base_url: formBaseUrl.value.trim(),
    api_key: formApiKey.value, // 空 = 保留原值(编辑时)
    auth_type: formAuthType.value,
    transport: formTransport.value,
    oauth_client_id: formClientId.value,
    oauth_client_secret: formClientSecret.value,
    oauth_scopes: formScopes.value,
    enabled: formEnabled.value,
  };
  try {
    if (editingId.value === null) {
      await mcpServerService.createServer(body);
      success('服务已保存并同步,发现的工具默认可用');
    } else {
      await mcpServerService.updateServer(editingId.value, body);
      success('服务已更新并重新同步');
    }
    resetForm();
    await Promise.all([loadServers(), loadTools()]);
  } catch (err) {
    error(err instanceof Error ? err.message : '保存失败,请检查地址与密钥');
  } finally {
    formSaving.value = false;
  }
};

const handleDeleteServer = async (server: MCPServerItem) => {
  if (!window.confirm(`确定删除服务「${server.name}」?它带来的 ${server.tool_count < 0 ? 0 : server.tool_count} 个外部工具将一并移除。`)) {
    return;
  }
  busyServerId.value = server.id;
  try {
    await mcpServerService.deleteServer(server.id);
    success(`已删除服务「${server.name}」`);
    await Promise.all([loadServers(), loadTools()]);
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
    await Promise.all([loadServers(), loadTools()]);
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

// 连接/断开开关:对应后端 enabled 状态。断开=目录即时失效(工具不可调用);
// 连接=立即同步发现工具。密钥等敏感字段留空 = 保留原值(后端语义)。
const handleToggleConnection = async (server: MCPServerItem) => {
  busyServerId.value = server.id;
  try {
    await mcpServerService.updateServer(server.id, {
      name: server.name,
      base_url: server.base_url,
      auth_type: server.auth_type,
      transport: (server.transport ?? '') as '' | 'streamable' | 'sse',
      enabled: !server.enabled,
    });
    success(server.enabled ? `已断开「${server.name}」` : `已连接「${server.name}」,工具已同步可用`);
    await loadServers();
  } catch (err) {
    error(err instanceof Error ? err.message : (server.enabled ? '断开失败' : '连接失败'));
  } finally {
    busyServerId.value = null;
  }
};

// 抽屉打开时拉清单
watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      loadTools();
      loadServers();
    }
  }
);
</script>

<template>
  <DialogShell :visible="visible" title="扩展能力" width="640px" @close="emit('close')">
    <!-- ===== tab 栏:工具 / 连接器 ===== -->
    <div class="cap-tabs">
      <button type="button" class="cap-tab" :class="{ active: activeTab === 'tools' }" @click="activeTab = 'tools'">工具</button>
      <button type="button" class="cap-tab" :class="{ active: activeTab === 'connectors' }" @click="activeTab = 'connectors'">
        连接器<span v-if="servers.length > 0" class="cap-tab-count">{{ servers.length }}</span>
      </button>
    </div>

    <!-- ===== 连接器 tab(MCP) ===== -->
    <div v-show="activeTab === 'connectors'">
    <div class="section-title">连接器</div>
    <p class="section-hint">接入 MCP 服务后,点开连接器可查看它提供的工具能力。密钥加密保存,不会明文显示。</p>

    <div v-if="serversLoading" class="hint-text">加载中...</div>
    <div v-else-if="servers.length === 0 && !showServerForm" class="hint-text">
      还没有接入外部服务
    </div>
    <ul v-else class="server-list">
      <li v-for="server in servers" :key="server.id" class="server-item">
        <!-- 信息区(点击展开/收起工具清单,同对话思考折叠交互) -->
        <div class="server-info" role="button" @click="toggleServerExpand(server.id)">
          <div class="server-name-row">
            <svg
              class="server-chevron"
              :class="{ open: expandedServers.has(server.id) }"
              width="14" height="14" viewBox="0 0 24 24" fill="none"
              stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
            ><polyline points="9 18 15 12 9 6" /></svg>
            <span class="server-name">{{ server.name }}</span>
            <span v-if="server.auth_type === 'oauth'" class="server-badge" :class="server.authorized ? '' : 'is-off'">
              {{ server.authorized ? '已授权' : '待授权' }}
            </span>
            <span class="server-badge" :class="{ 'is-off': !server.enabled }">
              {{ server.enabled ? '已连接' : '未连接' }}
            </span>
            <span class="server-count">{{ toolCountText(server) }}</span>
          </div>
          <div v-if="server.description" class="server-desc">{{ server.description }}</div>
          <div class="server-url">{{ server.base_url }}</div>
        </div>
        <!-- 操作区(常驻:连接开关 + 管理) -->
        <div class="server-actions">
          <button
            type="button"
            class="server-btn"
            :class="server.enabled ? '' : 'is-primary'"
            :disabled="busyServerId === server.id"
            @click="handleToggleConnection(server)"
          >{{ server.enabled ? '断开' : '连接' }}</button>
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
        <!-- 展开态:工具能力清单 -->
        <div v-if="expandedServers.has(server.id)" class="server-detail">
          <ul v-if="server.tools && server.tools.length > 0" class="server-tools">
            <li v-for="t in server.tools" :key="t.name" class="server-tool">
              <span class="server-tool-name">{{ t.name }}</span>
              <span class="server-tool-desc">{{ t.description }}</span>
            </li>
          </ul>
          <div v-else class="hint-text">该连接器暂未同步到工具,点「同步」重试</div>
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
        <label class="form-label" for="mcp-desc">能力概述 <span class="form-optional">选填</span></label>
        <input
          id="mcp-desc"
          v-model="formDescription"
          class="form-input"
          type="text"
          maxlength="256"
          placeholder="如:高德地图,提供天气 / POI 检索 / 路径规划能力"
        />
        <div class="form-hint">一句话概述这个连接器能做什么,有助于助手更准地匹配到它的工具。</div>
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
        <label class="form-label" for="mcp-transport">传输协议</label>
        <select id="mcp-transport" v-model="formTransport" class="form-input">
          <option value="">自动（默认 Streamable，失败自动回退 SSE）</option>
          <option value="streamable">Streamable HTTP（新协议）</option>
          <option value="sse">SSE（高德等平台端点）</option>
        </select>
        <div class="form-hint">高德：鉴权选「URL 参数」，Key 填在密钥框即可；或选 Bearer 并把 Key 写进地址(?key=…)。</div>
      </div>
      <div class="form-field">
        <label class="form-label" for="mcp-auth-type">鉴权方式</label>
        <select id="mcp-auth-type" v-model="formAuthType" class="form-input">
          <option value="bearer">API Key（Bearer 头）</option>
          <option value="query">URL 参数（key=…，高德等平台）</option>
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
    </div><!-- /连接器 tab -->

    <!-- ===== 工具 tab(内置 function call 工具) ===== -->
    <div v-show="activeTab === 'tools'" class="section-tools">
      <div class="section-title">工具</div>

      <div v-if="toolsLoading" class="hint-text">加载中...</div>
      <div v-else-if="tools.length === 0" class="hint-text">暂无可用工具</div>
      <ul v-else class="tool-list">
        <li
          v-for="tool in tools"
          :key="tool.name"
          class="tool-item"
          :class="{ 'is-disabled': !tool.available }"
        >
          <div class="tool-info">
            <div class="tool-name-row">
              <span class="tool-name">{{ tool.display_name || tool.name }}</span>
            </div>
            <div class="tool-desc">{{ tool.description }}</div>
          </div>
          <label class="tool-switch" :title="tool.available ? '' : '该工具暂不可用'">
            <input
              type="checkbox"
              role="switch"
              :aria-label="`启用${tool.display_name || tool.name}`"
              :checked="tool.enabled"
              :disabled="!tool.available || togglingTools.has(tool.name)"
              @change="handleToolToggle(tool, $event)"
            />
            <span class="tool-switch-slider"></span>
          </label>
        </li>
      </ul>
    </div>

    <div class="drawer-footer-spacer"></div>
  </DialogShell>
</template>

<style scoped>
/* ===== tab 栏(工具/连接器) ===== */
.cap-tabs {
  display: flex;
  gap: 4px;
  margin-bottom: 16px;
  padding: 3px;
  background: var(--bg-tip);
  border-radius: 8px;
}
.cap-tab {
  flex: 1;
  padding: 6px 12px;
  border: none;
  background: transparent;
  border-radius: 6px;
  font-size: 13px;
  font-family: inherit;
  color: var(--label-tertiary);
  cursor: pointer;
  transition: all 120ms ease;
}
.cap-tab.active {
  background: var(--bg-base);
  color: var(--label-primary);
  font-weight: 500;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
}
.cap-tab-count {
  margin-left: 6px;
  padding: 0 6px;
  background: var(--bg-hover);
  border-radius: 8px;
  font-size: 11px;
  color: var(--label-tertiary);
}

/* ===== 连接器工具折叠区 ===== */
.server-tools {
  list-style: none;
  margin: 8px 0 0;
  padding: 8px 12px;
  background: var(--bg-tip);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}
.server-tool {
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.server-tool-name {
  font-size: 12px;
  font-weight: 500;
  color: var(--label-primary);
  font-family: 'SF Mono', 'Menlo', monospace;
}
.server-tool-desc {
  font-size: 11px;
  color: var(--label-tertiary);
  line-height: 1.4;
}

.section-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--label-primary);
  margin-bottom: 8px;
}
.section-hint {
  font-size: 12px;
  color: var(--label-caption);
  margin: 0 0 12px;
  line-height: 1.5;
}
.hint-text {
  font-size: 13px;
  color: var(--label-caption);
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
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 10px 0;
  border-bottom: 0.5px solid var(--border-l1);
}
.server-item:last-child {
  border-bottom: none;
}
.server-info {
  flex: 1;
  min-width: 0;
  cursor: pointer;
}
.server-chevron {
  color: var(--label-caption);
  flex-shrink: 0;
  transition: transform 0.15s ease;
}
.server-chevron.open {
  transform: rotate(90deg);
}
.server-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.server-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--label-primary);
}
.server-badge {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--success-bg);
  color: var(--success);
  white-space: nowrap;
}
.server-badge.is-off {
  background: var(--code-bg);
  color: var(--label-caption);
}
.server-count {
  font-size: 11px;
  color: var(--label-caption);
  white-space: nowrap;
}
.server-desc {
  font-size: 12px;
  color: var(--label-primary);
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.server-url {
  font-size: 12px;
  color: var(--label-tertiary);
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.form-optional {
  font-size: 11px;
  font-weight: 400;
  color: var(--label-caption);
}
.server-detail {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.server-actions {
  display: flex;
  justify-content: flex-end;
  gap: 4px;
  flex-shrink: 0;
}
.server-btn {
  font-size: 12px;
  padding: 4px 8px;
  border-radius: 6px;
  border: 1px solid var(--border-l2);
  background: var(--bg-base);
  color: var(--label-secondary);
  cursor: pointer;
  font-family: inherit;
}
.server-btn:hover {
  border-color: var(--border-l3);
  background: var(--code-bg);
}
.server-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.server-btn.is-primary {
  color: var(--accent);
  border-color: rgba(34, 197, 94, 0.3);
}
.server-btn.is-primary:hover {
  background: var(--success-bg);
}
.server-btn.is-danger {
  color: var(--error);
  border-color: rgba(236, 19, 19, 0.25);
}
.server-btn.is-danger:hover {
  background: var(--error-bg);
}

/* ===== 表单 ===== */
.server-form {
  margin: 12px 0;
  padding: 12px;
  border: 1px solid var(--border-l2);
  border-radius: 10px;
  background: var(--code-bg);
}
.form-field {
  margin-bottom: 10px;
}
.form-label {
  display: block;
  font-size: 13px;
  color: var(--label-secondary);
  margin-bottom: 4px;
}
.form-input {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border-l3);
  border-radius: 8px;
  font-size: 14px;
  font-family: inherit;
  box-sizing: border-box;
}
.form-input:focus {
  outline: none;
  border-color: var(--accent);
}
.server-enable-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--label-secondary);
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
  border: 1px solid var(--border-l3);
  background: var(--bg-base);
  color: var(--label-secondary);
  cursor: pointer;
  font-family: inherit;
}
.form-btn.is-primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #ffffff;
}
.form-btn.is-primary:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
.add-server-btn {
  width: 100%;
  padding: 9px;
  border: 1px dashed var(--border-l3);
  border-radius: 10px;
  background: transparent;
  color: var(--label-tertiary);
  font-size: 13px;
  cursor: pointer;
  font-family: inherit;
  margin-bottom: 8px;
}
.add-server-btn:hover {
  border-color: var(--accent);
  color: var(--accent);
}

/* ===== 工具清单 ===== */
.section-tools {
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid var(--border-l1);
}
.tool-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.tool-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 0;
  border-bottom: 0.5px solid var(--border-l1);
}
.tool-item:last-child {
  border-bottom: none;
}
.tool-item.is-disabled .tool-name,
.tool-item.is-disabled .tool-desc {
  color: var(--label-caption);
}
.tool-info {
  flex: 1;
  min-width: 0;
}
.tool-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.tool-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--label-primary);
}
.tool-desc {
  font-size: 12px;
  color: var(--label-tertiary);
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

/* 开关(checkbox + slider) */
.tool-switch {
  position: relative;
  flex-shrink: 0;
  width: 36px;
  height: 20px;
  cursor: pointer;
}
.tool-switch input {
  position: absolute;
  opacity: 0;
  width: 100%;
  height: 100%;
  margin: 0;
  cursor: pointer;
}
.tool-switch input:disabled {
  cursor: not-allowed;
}
.tool-switch-slider {
  position: absolute;
  inset: 0;
  border-radius: 10px;
  background: var(--label-dimmed);
  transition: background 150ms ease;
  pointer-events: none;
}
.tool-switch-slider::before {
  content: '';
  position: absolute;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--bg-base);
  top: 2px;
  left: 2px;
  transition: transform 150ms ease;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2);
}
.tool-switch input:checked + .tool-switch-slider {
  background: var(--accent);
}
.tool-switch input:checked + .tool-switch-slider::before {
  transform: translateX(16px);
}
.tool-switch input:disabled + .tool-switch-slider {
  opacity: 0.5;
}

.drawer-footer-spacer {
  height: 32px;
}
</style>
