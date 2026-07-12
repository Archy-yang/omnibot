import type { ApiResponse } from '../types/api';

// v2.2: 飞书账号绑定相关接口
// 后端约定响应不含 success 字段(直接返业务对象),但走 axios 拦截器时
// 拦截器要求 data.success===true。后端 admin/web handler 统一用
// gin.H{"success":true,"data":...} 还是直接 gin.H{...}?
// 实测:feishu_bind_handler 用 c.JSON(code, bindingStatusResponse{...})
// 不含 success 字段 -> 拦截器会 reject。
// 所以这里与 auth.ts 一样用裸 fetch 绕过拦截器。

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

export interface FeishuBindingStatus {
  bound: boolean;
}

export interface FeishuBindCode {
  code: string;
  expires_at: string;
  expires_in: number;
}

export const feishuService = {
  async getBindingStatus(): Promise<FeishuBindingStatus> {
    return authedJSON<FeishuBindingStatus>('GET', '/user/feishu/binding');
  },

  async generateBindCode(): Promise<FeishuBindCode> {
    return authedJSON<FeishuBindCode>('POST', '/user/feishu/bind-code');
  },
};

export default feishuService;

// 占位防 tsc 未用
export type { ApiResponse };
