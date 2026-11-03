import { request } from '../utils/request';
import type { ApiResponse, SearchConfigResponse, UpdateSearchConfigRequest } from '../types/api';

// 32 号:用户级联网搜索配置。身份由 JWT 中间件解析,前端不传 user_id。
export const searchConfigService = {
  async getConfig(): Promise<SearchConfigResponse> {
    const response = await request.get<ApiResponse<SearchConfigResponse>>('/user/search-config');
    return response.data.data;
  },

  async saveConfig(payload: UpdateSearchConfigRequest): Promise<SearchConfigResponse> {
    const response = await request.put<ApiResponse<SearchConfigResponse>>('/user/search-config', payload);
    return response.data.data;
  },

  async clearConfig(): Promise<void> {
    await request.delete<ApiResponse<void>>('/user/search-config');
  },
};
