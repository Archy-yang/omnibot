import { request } from '../utils/request';
import type { ApiResponse, Config, UpdateConfigRequest, UserLLMConfigResponse, UpdateUserLLMConfigRequest } from '../types/api';

export const configService = {
  async getConfig(): Promise<Config> {
    try {
      const response = await request.get<ApiResponse<Config>>('/config');
      return response.data.data;
    } catch (error) {
      console.error('Failed to get config:', error);
      throw error;
    }
  },

  async updateConfig(config: UpdateConfigRequest): Promise<void> {
    try {
      await request.put<ApiResponse<void>>('/config', config);
    } catch (error) {
      console.error('Failed to update config:', error);
      throw error;
    }
  },

  // ========== 用户级 LLM 配置接口 ==========

  async getUserLLMConfig(sessionId: string): Promise<UserLLMConfigResponse> {
    try {
      const response = await request.get<ApiResponse<UserLLMConfigResponse>>('/user/llm-config', {
        params: { session_id: sessionId }
      });
      return response.data.data;
    } catch (error) {
      console.error('Failed to get user LLM config:', error);
      throw error;
    }
  },

  async updateUserLLMConfig(config: UpdateUserLLMConfigRequest): Promise<void> {
    try {
      await request.put<ApiResponse<void>>('/user/llm-config', config);
    } catch (error) {
      console.error('Failed to update user LLM config:', error);
      throw error;
    }
  },

  async deleteUserLLMConfig(sessionId: string): Promise<void> {
    try {
      await request.delete<ApiResponse<void>>('/user/llm-config', {
        params: { session_id: sessionId }
      });
    } catch (error) {
      console.error('Failed to delete user LLM config:', error);
      throw error;
    }
  },
};

export default configService;
