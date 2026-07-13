import axios, { type AxiosError } from 'axios';
import type { AxiosInstance, AxiosRequestConfig, AxiosResponse, InternalAxiosRequestConfig } from 'axios';
import type { ApiResponse } from '../types/api';

const BASE_URL = import.meta.env.VITE_API_BASE_URL || '/api/v1';

const instance: AxiosInstance = axios.create({
  baseURL: BASE_URL,
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
});

instance.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    const token = localStorage.getItem('token');
    if (token && config.headers) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  },
  (error: AxiosError) => {
    return Promise.reject(error);
  }
);

instance.interceptors.response.use(
  <T = unknown>(response: AxiosResponse<ApiResponse<T>>) => {
    const { data } = response;
    if (data && data.success) {
      return response;
    }
    return Promise.reject(new Error(data?.error || 'Request failed'));
  },
  (error: AxiosError) => {
    if (error.response) {
      const { status, data } = error.response;
      switch (status) {
        case 401:
          // /auth/* 的 401 是登录/注册失败,用户已在登录页,不跳转避免死循环;
          // 其他端点 401 = token 失效,清 token 跳登录
          if (!error.config?.url?.startsWith('/auth/')) {
            localStorage.removeItem('token');
            window.location.href = '/login';
          }
          break;
        case 403:
          console.error('Permission denied');
          break;
        case 404:
          console.error('Resource not found');
          break;
        case 500:
          console.error('Server error');
          break;
        default:
          console.error('Unknown error');
      }
      // 后端统一响应:{success, error?};错误文案在 data.error
      const errorMessage = (data as { error?: string })?.error || (data as { message?: string })?.message || error.message;
      return Promise.reject(new Error(errorMessage));
    }
    if (error.request) {
      return Promise.reject(new Error('Network error, please check your connection'));
    }
    return Promise.reject(error);
  }
);

export const request = {
  get<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<AxiosResponse<T>> {
    return instance.get<unknown, AxiosResponse<T>>(url, config);
  },

  post<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<AxiosResponse<T>> {
    return instance.post<unknown, AxiosResponse<T>>(url, data, config);
  },

  put<T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<AxiosResponse<T>> {
    return instance.put<unknown, AxiosResponse<T>>(url, data, config);
  },

  delete<T = unknown>(url: string, config?: AxiosRequestConfig): Promise<AxiosResponse<T>> {
    return instance.delete<unknown, AxiosResponse<T>>(url, config);
  },
};

export default instance;
