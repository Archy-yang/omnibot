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

export const memoryService = {
  async getMemories(sessionId: string): Promise<GetMemoriesResponse> {
    try {
      const response = await request.get<ApiResponse<GetMemoriesResponse>>('/memories', {
        params: { session_id: sessionId },
      });
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

  async clearMemories(sessionId: string): Promise<ClearMemoriesResponse> {
    try {
      const response = await request.delete<ApiResponse<ClearMemoriesResponse>>('/memories', {
        params: { session_id: sessionId },
      });
      return response.data.data;
    } catch (error) {
      console.error('Failed to clear memories:', error);
      throw error;
    }
  },

  async deleteMemory(id: number, sessionId: string): Promise<DeleteMemoryResponse> {
    try {
      const response = await request.delete<ApiResponse<DeleteMemoryResponse>>(`/memories/${id}`, {
        params: { session_id: sessionId },
      });
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
