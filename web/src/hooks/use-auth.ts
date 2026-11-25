/**
 * 登录态 hook：订阅 lib/auth 的 localStorage 恢复与全局变更。
 * 不存"authStore"——token 存 localStorage 后由此 hook（context 化留 F3）持有解析出的 user。
 */

import { useEffect, useState } from 'react';
import {
  getAuthUser,
  isLoggedIn,
  subscribeAuth,
  clearAuth,
  type AuthUser,
} from '@/lib/auth';

export interface AuthState {
  loggedIn: boolean;
  user: AuthUser | null;
  logout: () => void;
}

export function useAuth(): AuthState {
  const [user, setUser] = useState<AuthUser | null>(() => getAuthUser());

  useEffect(() => {
    // 刷新页面恢复 + 登录/登出/401 清空时同步
    setUser(getAuthUser());
    return subscribeAuth(() => setUser(getAuthUser()));
  }, []);

  return {
    loggedIn: isLoggedIn(),
    user,
    logout: () => clearAuth(),
  };
}
