/**
 * styleProfile — 风格与认知画像 API(24 号 §5 + M2 注入开关)
 *
 * - GET /style-profile 画像视图(观察 + 认知变化)+ injection_enabled 开关态
 * - PUT /style-profile/injection 注入开关(M2:默认关,显式开启;关 = 完全回通用 persona)
 * - DELETE /style-profile/observation/:id 删除单条观察(纠正口)
 * - DELETE /style-profile/change/:id 删除单条认知变化
 */
import { request } from '../utils/request';
import type { ApiResponse, StyleProfileView } from '../types/api';

/** 画像视图 + 注入开关态(GET /style-profile 一次带回) */
export interface StyleProfileWithInjection {
  profile: StyleProfileView;
  injectionEnabled: boolean;
}

export const styleProfileService = {
  async getProfile(): Promise<StyleProfileWithInjection> {
    const response = await request.get<ApiResponse<StyleProfileView> & { injection_enabled?: boolean }>(
      '/style-profile',
    );
    return {
      profile: response.data.data,
      injectionEnabled: response.data.injection_enabled ?? false,
    };
  },

  async setInjection(enabled: boolean): Promise<void> {
    await request.put<ApiResponse<null>>('/style-profile/injection', { enabled });
  },

  async deleteObservation(id: number): Promise<void> {
    await request.delete<ApiResponse<null>>(`/style-profile/observation/${id}`);
  },

  async deleteChange(id: number): Promise<void> {
    await request.delete<ApiResponse<null>>(`/style-profile/change/${id}`);
  },
};

export default styleProfileService;
