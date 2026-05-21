/**
 * 用户信息接口
 */
export interface User {
  id: string;
  name: string;
  avatar?: string;
  email?: string;
}

/**
 * 用户状态接口
 */
export interface UserState {
  user: User | null;
  isAuthenticated: boolean;
}
