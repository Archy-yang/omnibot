import type { ApiResponse } from '../types/api';

// v2.1: 邮箱密码认证服务
// 后端约定:注册/登录都返回 { token: "..." }

interface AuthResponse {
  token: string;
}

// 注册/登录接口不经 axios 拦截器的成功校验(因为后端返回 { token } 不含 success 字段)
// 所以我们直接用底层 fetch,或者绕过 request 的 interceptor。
// 更简单:让 request 拦截器兼容(response.data.success === undefined 时视为原样返回)。
// 现有拦截器要求 data.success===true 才通过;为了不改拦截器全局行为,这里改用裸 fetch。

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1';

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg = (data && (data.error || data.message)) || `HTTP ${res.status}`;
    throw new Error(msg);
  }
  return data as T;
}

export const authService = {
  async register(email: string, password: string, confirmPassword: string): Promise<string> {
    const data = await postJSON<AuthResponse>('/auth/register', {
      email,
      password,
      confirm_password: confirmPassword,
    });
    return data.token;
  },

  async login(email: string, password: string): Promise<string> {
    const data = await postJSON<AuthResponse>('/auth/login', {
      email,
      password,
    });
    return data.token;
  },
};

// 保留占位,避免 tsc 未使用报警
export type { ApiResponse };

export default authService;
