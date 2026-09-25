import { request } from '../utils/request';
import type {
  ApiResponse,
  ListToolsResponse,
  UpdateToolsResponse,
} from '../types/api';

/**
 * 工具管理服务(13-插件系统):清单 + 启停。
 * 启停即时生效(后端重建工具池),无需刷新对话。
 */
export const toolService = {
  async listTools(): Promise<ListToolsResponse> {
    try {
      const response = await request.get<ApiResponse<ListToolsResponse>>('/tools');
      return response.data.data;
    } catch (error) {
      console.error('Failed to list tools:', error);
      throw error;
    }
  },

  async updateTool(name: string, enabled: boolean): Promise<UpdateToolsResponse> {
    try {
      const response = await request.put<ApiResponse<UpdateToolsResponse>>(
        `/tools/${encodeURIComponent(name)}`,
        { enabled }
      );
      return response.data.data;
    } catch (error) {
      console.error('Failed to update tool:', error);
      throw error;
    }
  },
};

export default toolService;
