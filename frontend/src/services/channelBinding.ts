// v2.3: 账号绑定相关接口(渠道通用,飞书+微信)
// 后端 /user/channel-binding/* 返业务对象不含 success 字段,
// 走 axios 拦截器会被 reject,故与 auth.ts 同款用裸 fetch。

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1';

async function authedJSON<T>(method: string, path: string): Promise<T> {
  const token = localStorage.getItem('token');
  const headers: Record<string, string> = {};
  if (token) headers.Authorization = `Bearer ${token}`;

  const res = await fetch(`${BASE_URL}${path}`, { method, headers });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg = (data && (data.error || data.message)) || `HTTP ${res.status}`;
    if (res.status === 401) {
      localStorage.removeItem('token');
      window.location.href = '/login';
    }
    throw new Error(msg);
  }
  return data as T;
}

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
    return authedJSON<ChannelBindingStatus>('GET', '/user/channel-binding');
  },

  // 生成通用绑定码(不区分渠道,在哪个渠道发绑哪个)
  async generateBindCode(): Promise<BindCode> {
    return authedJSON<BindCode>('POST', '/user/channel-binding/bind-code');
  },
};

export default channelBindingService;
