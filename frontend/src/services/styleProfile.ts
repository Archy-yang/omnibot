/**
 * styleProfile — 风格与认知画像 API(24 号 §5)
 *
 * - GET /style-profile 画像视图(观察 + 认知变化)
 * - DELETE /style-profile/observation/:id 删除单条观察(纠正口)
 * - DELETE /style-profile/change/:id 删除单条认知变化
 */
import { request } from '../utils/request';
import type { ApiResponse, StyleProfileView } from '../types/api';

export const styleProfileService = {
  async getProfile(): Promise<StyleProfileView> {
    const response = await request.get<ApiResponse<StyleProfileView>>('/style-profile');
    return response.data.data;
  },

  async deleteObservation(id: number): Promise<void> {
    await request.delete<ApiResponse<null>>(`/style-profile/observation/${id}`);
  },

  async deleteChange(id: number): Promise<void> {
    await request.delete<ApiResponse<null>>(`/style-profile/change/${id}`);
  },
};

export default styleProfileService;
