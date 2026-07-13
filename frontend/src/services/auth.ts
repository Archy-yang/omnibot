import { request } from '../utils/request';
import type { ApiResponse } from '../types/api';

// v2.1 邮箱密码认证服务
// 后端响应统一走 {success, data/error} 外壳(与老端点一致),经 axios 拦截器解包。
// /auth/* 的 401(登录/注册失败)由拦截器豁免跳转,直接 reject(Error(后端文案))。

interface AuthData {
  token: string;
}

export const authService = {
  async register(email: string, password: string, confirmPassword: string): Promise<string> {
    const resp = await request.post<ApiResponse<AuthData>>('/auth/register', {
      email,
      password,
      confirm_password: confirmPassword,
    });
    // 拦截器已保证 success=true,resp.data.data.token 即业务数据
    return resp.data.data.token;
  },

  async login(email: string, password: string): Promise<string> {
    const resp = await request.post<ApiResponse<AuthData>>('/auth/login', {
      email,
      password,
    });
    return resp.data.data.token;
  },
};

export default authService;
