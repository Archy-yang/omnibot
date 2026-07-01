import { request } from '../utils/request';
import type { ApiResponse, Config, UpdateConfigRequest, UserLLMConfigResponse, UpdateUserLLMConfigRequest } from '../types/api';
import type { UserLLMProvidersResponse } from '../types/api';

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
  // v2.1: 身份由 JWT 中间件解析,前端不再传 session_id

  async getUserLLMConfig(): Promise<UserLLMConfigResponse> {
    try {
      const response = await request.get<ApiResponse<UserLLMConfigResponse>>('/user/llm-config');
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

  async deleteUserLLMConfig(): Promise<void> {
    try {
      await request.delete<ApiResponse<void>>('/user/llm-config');
    } catch (error) {
      console.error('Failed to delete user LLM config:', error);
      throw error;
    }
  },

  // ========== LLM 服务商预设配置接口 ==========

  async getUserLLMProviders(): Promise<UserLLMProvidersResponse> {
    try {
      const response = await request.get<ApiResponse<UserLLMProvidersResponse>>('/user/llm-providers');
      return response.data.data;
    } catch (error) {
      console.error('Failed to get LLM providers:', error);
      throw error;
    }
  },
};

export default configService;
