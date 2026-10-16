import { request } from '../utils/request';
import type { ApiResponse } from '../types/api';

/**
 * 主动管家(26 号:每日主动关心)。默认关闭,显式开启才工作;
 * 每天最多主动找你一次,只在真有事时开口。
 */
export interface ProactiveSettings {
  enabled: boolean;
  /** 每日主动关心时刻(0-23,默认 9);22:00-08:00 触发会被服务端拒绝 */
  hour: number;
}

export interface ProactiveNoteItem {
  id: number;
  /** matter_due / loop_stale / recent_followup */
  kind: string;
  content: string;
  /** delivered=已送达;skipped=裁决通过但被克制闸门拦下 */
  status: string;
  dedup_key: string;
  matter_id?: number;
  created_at: string;
}

export const proactiveService = {
  /** 读设置 */
  async getSettings(): Promise<ProactiveSettings> {
    const response = await request.get<ApiResponse<ProactiveSettings>>(
      '/proactive-settings',
    );
    return response.data.data;
  },

  /** 写设置(hour 不传则保留现值) */
  async updateSettings(enabled: boolean, hour?: number): Promise<ProactiveSettings> {
    const response = await request.put<ApiResponse<ProactiveSettings>>(
      '/proactive-settings',
      { enabled, hour },
    );
    return response.data.data;
  },

  /** 最近主动记录(创建时间倒序,最多 50) */
  async listNotes(limit = 20): Promise<ProactiveNoteItem[]> {
    const response = await request.get<ApiResponse<{ notes: ProactiveNoteItem[] }>>(
      '/proactive-notes',
      { params: { limit } },
    );
    return response.data.data.notes;
  },
};
