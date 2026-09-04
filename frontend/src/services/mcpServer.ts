import { request } from '../utils/request';
import type {
  ApiResponse,
  ListMCPServersResponse,
  SyncMCPServerResponse,
  UpsertMCPServerRequest,
} from '../types/api';
import type { MCPServerItem } from '../types/api';

/**
 * MCP server 在线管理服务(13-插件系统 M3):增删改查 + 手动同步。
 * 增改会立即触发同步(连接→发现工具→落技能清单,默认停用);失败以返回值 err 表达。
 */
export const mcpServerService = {
  async listServers(): Promise<ListMCPServersResponse> {
    try {
      const response = await request.get<ApiResponse<ListMCPServersResponse>>('/mcp/servers');
      return response.data.data;
    } catch (error) {
      console.error('Failed to list MCP servers:', error);
      throw error;
    }
  },

  async createServer(body: UpsertMCPServerRequest): Promise<{ server: MCPServerItem }> {
    try {
      const response = await request.post<ApiResponse<{ server: MCPServerItem }>>('/mcp/servers', body);
      return response.data.data;
    } catch (error) {
      console.error('Failed to create MCP server:', error);
      throw error;
    }
  },

  async updateServer(id: number, body: UpsertMCPServerRequest): Promise<{ server: MCPServerItem }> {
    try {
      const response = await request.put<ApiResponse<{ server: MCPServerItem }>>(`/mcp/servers/${id}`, body);
      return response.data.data;
    } catch (error) {
      console.error('Failed to update MCP server:', error);
      throw error;
    }
  },

  async deleteServer(id: number): Promise<void> {
    try {
      await request.delete(`/mcp/servers/${id}`);
    } catch (error) {
      console.error('Failed to delete MCP server:', error);
      throw error;
    }
  },

  async syncServer(id: number): Promise<SyncMCPServerResponse> {
    try {
      const response = await request.post<ApiResponse<SyncMCPServerResponse>>(`/mcp/servers/${id}/sync`);
      return response.data.data;
    } catch (error) {
      console.error('Failed to sync MCP server:', error);
      throw error;
    }
  },
};

export default mcpServerService;
