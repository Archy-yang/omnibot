import { request } from '../utils/request';
import type { ApiResponse } from '../types/api';

// v2.3 账号绑定相关接口(渠道通用,飞书+微信)
// 后端响应统一走 {success, data/error} 外壳,经 axios 拦截器解包。

// 各渠道绑定状态(v2.3: 飞书 + 微信)
export interface ChannelBindingStatus {
  feishu_bound: boolean;
  wechat_bound: boolean;
}

export interface BindCode {
  code: string;
  expires_at: string;
  expires_in: number;
}

export const channelBindingService = {
  // 查询各渠道绑定状态
  async getBindingStatus(): Promise<ChannelBindingStatus> {
    const resp = await request.get<ApiResponse<ChannelBindingStatus>>('/user/channel-binding');
    return resp.data.data;
  },

  // 生成通用绑定码(不区分渠道,在哪个渠道发绑哪个)
  async generateBindCode(): Promise<BindCode> {
    const resp = await request.post<ApiResponse<BindCode>>('/user/channel-binding/bind-code');
    return resp.data.data;
  },
};

export default channelBindingService;
