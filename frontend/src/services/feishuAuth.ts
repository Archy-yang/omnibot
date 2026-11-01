import { request } from '../utils/request';
import type { ApiResponse, FeishuAuthView } from '../types/api';

/**
 * 用户级飞书授权服务(19-多用户LarkCLI授权):发起 device flow / 轮询收尾 / 断开。
 * 授权后 feishu 工具自动按当前用户身份执行,无需重启。
 */
export const feishuAuthService = {
  async getStatus(): Promise<FeishuAuthView> {
    const response = await request.get<ApiResponse<FeishuAuthView>>('/feishu-auth');
    return response.data.data;
  },

  async startAuth(appId: string, appSecret: string): Promise<FeishuAuthView> {
    const response = await request.post<ApiResponse<FeishuAuthView>>('/feishu-auth', {
      app_id: appId,
      app_secret: appSecret,
    });
    return response.data.data;
  },

  /**
   * 用后端已存的 AppID/AppSecret 重新发起 device flow(19 号补强)。
   * token 事后失效时无需用户再翻后台找 secret。
   */
  async relogin(): Promise<FeishuAuthView> {
    const response = await request.post<ApiResponse<FeishuAuthView>>('/feishu-auth/relogin');
    return response.data.data;
  },

  async poll(): Promise<FeishuAuthView> {
    const response = await request.get<ApiResponse<FeishuAuthView>>('/feishu-auth/poll');
    return response.data.data;
  },

  async disconnect(): Promise<void> {
    await request.delete<ApiResponse<null>>('/feishu-auth');
  },
};

export default feishuAuthService;
