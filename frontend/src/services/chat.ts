import { request } from '../utils/request';
import type { Message } from '../types/chat';
import type { ApiResponse, GetHistoryResponse, PaginationParams, SendMessageRequest } from '../types/api';

export const chatService = {
  async sendMessage(content: string, sessionId: string): Promise<Message> {
    try {
      const requestData: SendMessageRequest = {
        content,
        session_id: sessionId,
      };

      const response = await request.post<ApiResponse<Message>>('/chat/messages', requestData);
      return response.data.data;
    } catch (error) {
      console.error('Failed to send message:', error);
      throw error;
    }
  },

  async getHistory(
    sessionId: string,
    params?: PaginationParams
  ): Promise<Message[]> {
    try {
      const response = await request.get<ApiResponse<GetHistoryResponse>>('/chat/messages', {
        params: {
          session_id: sessionId,
          limit: params?.limit || 50,
          before: params?.before,
        },
      });
      return response.data.data.messages;
    } catch (error) {
      console.error('Failed to get chat history:', error);
      throw error;
    }
  },
};

export default chatService;
