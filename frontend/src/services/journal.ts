/**
 * journal — 个人经历层 API(21 号 §5)
 *
 * - GET /memories/journal 时间轴(kind/from/to/cursor/limit,cursor 翻页)
 * - GET /matters 档案列表(status 过滤)
 * - GET /matters/:id 档案详情 + 演进时间线
 * - PUT /matters/:id 用户纠正口(仅 state_desc/status)
 */
import { request } from '../utils/request';
import type { ApiResponse, JournalPage, MatterDetail, MatterSummary } from '../types/api';

export interface JournalQuery {
  kind?: 'all' | 'fact' | 'loop';
  from?: string;
  to?: string;
  cursor?: string;
  limit?: number;
}

export interface UpdateMatterPayload {
  state_desc?: string;
  status?: string;
}

export const journalService = {
  async getJournal(query: JournalQuery = {}): Promise<JournalPage> {
    const params = new URLSearchParams();
    if (query.kind && query.kind !== 'all') params.set('kind', query.kind);
    if (query.from) params.set('from', query.from);
    if (query.to) params.set('to', query.to);
    if (query.cursor) params.set('cursor', query.cursor);
    if (query.limit) params.set('limit', String(query.limit));
    const qs = params.toString();
    const response = await request.get<ApiResponse<JournalPage>>(`/memories/journal${qs ? `?${qs}` : ''}`);
    return response.data.data;
  },

  async listMatters(status?: string): Promise<MatterSummary[]> {
    const url = status ? `/matters?status=${status}` : '/matters';
    const response = await request.get<ApiResponse<MatterSummary[]>>(url);
    return response.data.data;
  },

  async getMatter(id: number): Promise<MatterDetail> {
    const response = await request.get<ApiResponse<MatterDetail>>(`/matters/${id}`);
    return response.data.data;
  },

  async updateMatter(id: number, payload: UpdateMatterPayload): Promise<void> {
    await request.put<ApiResponse<null>>(`/matters/${id}`, payload);
  },
};

export default journalService;
