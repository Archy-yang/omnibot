import { request } from '../utils/request';
import type {
  ApiResponse,
  ListSubscriptionsResponse,
  AddSubscriptionResponse,
} from '../types/api';

/**
 * 订阅源管理服务(14-订阅源管理技术方案):清单/订阅/退订/暂停恢复。
 * 订阅时后端自动发现 RSS 地址;多候选时返回 candidates 让用户挑。
 */
export const subscriptionService = {
  async list(): Promise<ListSubscriptionsResponse> {
    const response = await request.get<ApiResponse<ListSubscriptionsResponse>>('/subscriptions');
    return response.data.data;
  },

  async add(url: string, topicDesc = ''): Promise<AddSubscriptionResponse> {
    const response = await request.post<ApiResponse<AddSubscriptionResponse>>('/subscriptions', {
      url,
      topic_desc: topicDesc,
    });
    return response.data.data;
  },

  async addCandidate(feedUrl: string): Promise<AddSubscriptionResponse> {
    // 用户从候选中选定后,直接以 feed 地址再订阅(后端 Validate 捷径命中)
    return this.add(feedUrl);
  },

  async remove(id: number): Promise<void> {
    await request.delete<ApiResponse<void>>(`/subscriptions/${id}`);
  },

  async setStatus(id: number, status: 'active' | 'paused'): Promise<void> {
    await request.put<ApiResponse<void>>(`/subscriptions/${id}/status`, { status });
  },
};

export default subscriptionService;
