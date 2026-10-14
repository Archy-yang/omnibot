/**
 * memoryRules — 沉淀黑名单 API(22号 §5.3)
 *
 * - GET /memory-rules 规则清单
 * - POST /memory-rules 新增规则(同时删除存量匹配记忆,返回删除条数)
 * - DELETE /memory-rules/:id 解除规则(存量不恢复)
 */
import { request } from '../utils/request';
import type { ApiResponse, MemoryRule } from '../types/api';

export interface AddRuleResult {
  rule: MemoryRule;
  deleted: number;
}

export const memoryRuleService = {
  async listRules(): Promise<MemoryRule[]> {
    const response = await request.get<ApiResponse<MemoryRule[]>>('/memory-rules');
    return response.data.data;
  },

  async addRule(pattern: string): Promise<AddRuleResult> {
    const response = await request.post<ApiResponse<AddRuleResult>>('/memory-rules', { pattern });
    return response.data.data;
  },

  async removeRule(id: number): Promise<void> {
    await request.delete<ApiResponse<null>>(`/memory-rules/${id}`);
  },
};

export default memoryRuleService;
