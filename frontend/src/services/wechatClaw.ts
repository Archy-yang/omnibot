import { request } from '../utils/request';
import type { ApiResponse } from '../types/api';

/**
 * 微信 ClawBot 接入服务(31 号技术方案)。
 *
 * 扫码登录是单账号、进程级:后端拿到 confirmed 后立即落库并拉起长轮询,
 * 前端只需要「申请二维码 → 轮询状态 → 展示连接结果」。
 */
export interface WeChatClawLoginView {
  qrcode: string;
  qr_url: string;
  expires_in: number;
}

export interface WeChatClawPollView {
  status: string;
  connected: boolean;
  need_verify_code: boolean;
}

export interface WeChatClawStatusView {
  connected: boolean;
  bot_id?: string;
  status?: string;
  need_relogin?: boolean;
}

export const wechatClawService = {
  /** 申请登录二维码(qr_url 需由前端渲染成二维码图片)。 */
  async startLogin(): Promise<WeChatClawLoginView> {
    const response = await request.post<ApiResponse<WeChatClawLoginView>>('/wechat-claw/login');
    return response.data.data;
  },

  /** 轮询扫码状态。 */
  async pollLogin(
    qrcode: string,
    verifyCode?: string,
  ): Promise<{ view: WeChatClawPollView; connected: boolean }> {
    const response = await request.get<
      ApiResponse<WeChatClawPollView> & { connected?: boolean }
    >(`/wechat-claw/login/${encodeURIComponent(qrcode)}`, {
      params: verifyCode ? { verify_code: verifyCode } : undefined,
    });
    return {
      view: response.data.data,
      connected: Boolean(response.data.connected),
    };
  },

  /** 当前连接状态。 */
  async getStatus(): Promise<WeChatClawStatusView> {
    const response = await request.get<ApiResponse<WeChatClawStatusView>>('/wechat-claw/status');
    return response.data.data;
  },

  /** 断开连接(停长轮询 + 清空凭证)。 */
  async disconnect(): Promise<void> {
    await request.delete<ApiResponse<null>>('/wechat-claw');
  },
};

export default wechatClawService;
