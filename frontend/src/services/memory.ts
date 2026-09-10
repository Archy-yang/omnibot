import { request } from '../utils/request';
import type {
  ApiResponse,
  ClearMemoriesResponse,
  CreateMemoryRequest,
  CreateMemoryResponse,
  DeleteMemoryResponse,
  GetMemoriesResponse,
  UpdateMemoryRequest,
  UpdateMemoryResponse,
} from '../types/api';

// v2.1: 身份由 JWT 中间件解析,前端不再传 session_id
export const memoryService = {
  async getMemories(): Promise<GetMemoriesResponse> {
    try {
      const response = await request.get<ApiResponse<GetMemoriesResponse>>('/memories');
      return response.data.data;
    } catch (error) {
      console.error('Failed to get memories:', error);
      throw error;
    }
  },

  async createMemory(requestBody: CreateMemoryRequest): Promise<CreateMemoryResponse> {
    try {
      const response = await request.post<ApiResponse<CreateMemoryResponse>>('/memories', requestBody);
      return response.data.data;
    } catch (error) {
      console.error('Failed to create memory:', error);
      throw error;
    }
  },

  async clearMemories(source?: 'manual' | 'auto'): Promise<ClearMemoriesResponse> {
    try {
      // 带 source 只清该来源(记忆抽屉双 tab);不带清空全部(渠道 #清空记忆 语义)
      const url = source ? `/memories?source=${source}` : '/memories';
      const response = await request.delete<ApiResponse<ClearMemoriesResponse>>(url);
      return response.data.data;
    } catch (error) {
      console.error('Failed to clear memories:', error);
      throw error;
    }
  },

  async deleteMemory(id: number): Promise<DeleteMemoryResponse> {
    try {
      const response = await request.delete<ApiResponse<DeleteMemoryResponse>>(`/memories/${id}`);
      return response.data.data;
    } catch (error) {
      console.error('Failed to delete memory:', error);
      throw error;
    }
  },

  async updateMemory(id: number, requestBody: UpdateMemoryRequest): Promise<UpdateMemoryResponse> {
    try {
      const response = await request.put<ApiResponse<UpdateMemoryResponse>>(`/memories/${id}`, requestBody);
      return response.data.data;
    } catch (error) {
      console.error('Failed to update memory:', error);
      throw error;
    }
  },
};

export default memoryService;
