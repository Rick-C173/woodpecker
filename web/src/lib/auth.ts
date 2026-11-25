/**
 * 认证信息存取（localStorage，架构 §4.1）。
 * - token 与用户信息分开两个 key；刷新页面直接读 localStorage 恢复登录态，
 *   不额外调 /me 类接口校验——首次 401 时由 client 全局兜底踢出。
 * - 全局事件总线：client 中间件在 401 / AUTH_007 时广播，路由层统一导航。
 */

export interface AuthUser {
  user_id: number;
  username: string;
  is_admin: boolean;
  must_change_password: boolean;
}

const TOKEN_KEY = 'wp-token';
const USER_KEY = 'wp-user';

/** 全局认证事件：401 → 'logout'（清 token 跳登录带 redirect）；403 AUTH_007 → 'change-password' */
export type AuthEvent = 'logout' | 'change-password';

type AuthListener = () => void;
type AuthEventListener = (event: AuthEvent) => void;

const listeners = new Set<AuthListener>();
const eventListeners = new Set<AuthEventListener>();

function safeGet(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function safeSet(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* 隐私模式静默失败 */
  }
}

function safeRemove(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {
    /* 静默失败 */
  }
}

function notify(): void {
  for (const fn of listeners) fn();
}

export function getToken(): string | null {
  return safeGet(TOKEN_KEY);
}

export function getAuthUser(): AuthUser | null {
  const raw = safeGet(USER_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<AuthUser>;
    if (
      typeof parsed.user_id === 'number' &&
      typeof parsed.username === 'string' &&
      typeof parsed.is_admin === 'boolean' &&
      typeof parsed.must_change_password === 'boolean'
    ) {
      return {
        user_id: parsed.user_id,
        username: parsed.username,
        is_admin: parsed.is_admin,
        must_change_password: parsed.must_change_password,
      };
    }
    return null;
  } catch {
    return null;
  }
}

export function isLoggedIn(): boolean {
  return getToken() !== null && getAuthUser() !== null;
}

/** 登录成功后写入认证信息（token 与用户信息分开两个 key）。 */
export function setAuth(token: string, user: AuthUser): void {
  safeSet(TOKEN_KEY, token);
  safeSet(USER_KEY, JSON.stringify(user));
  notify();
}

/** 清空认证信息（登出 / 401 全局兜底）。 */
export function clearAuth(): void {
  safeRemove(TOKEN_KEY);
  safeRemove(USER_KEY);
  notify();
}

/** 订阅登录态变化（AuthProvider / 守卫用）。 */
export function subscribeAuth(listener: AuthListener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** 全局认证事件：client 中间件广播，路由层监听导航。 */
export function emitAuthEvent(event: AuthEvent): void {
  for (const fn of eventListeners) fn(event);
}

export function onAuthEvent(listener: AuthEventListener): () => void {
  eventListeners.add(listener);
  return () => eventListeners.delete(listener);
}

/** 当前页面路径（登录 redirect 参数用）。 */
export function currentPathWithFrom(): string {
  const path = typeof window !== 'undefined' ? window.location.pathname : '/';
  return `/login?from=${encodeURIComponent(path)}`;
}
