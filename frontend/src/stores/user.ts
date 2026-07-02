import { defineStore } from 'pinia';
import { computed, ref } from 'vue';

const TOKEN_KEY = 'token';

// v2.1: 基于 JWT 的认证 store。
// token 是唯一权威身份来源:存在且未过期即视为已登录。
// 后端在 401 时会由 axios 拦截器 / chat.ts 手动兜底清 token + 跳 /login。
export const useAuthStore = defineStore('auth', () => {
  const token = ref<string>(localStorage.getItem(TOKEN_KEY) || '');

  const isAuthenticated = computed<boolean>(() => !!token.value);

  const setToken = (newToken: string): void => {
    token.value = newToken;
    localStorage.setItem(TOKEN_KEY, newToken);
  };

  const clearToken = (): void => {
    token.value = '';
    localStorage.removeItem(TOKEN_KEY);
  };

  return {
    token,
    isAuthenticated,
    setToken,
    clearToken,
  };
});
