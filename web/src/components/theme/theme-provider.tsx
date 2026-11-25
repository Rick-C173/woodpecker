/**
 * 明暗主题 Provider：wp-theme ∈ {light, dark, system}，默认 system。
 * - system：按 matchMedia('(prefers-color-scheme: dark)') 解析并监听 change 动态跟随；
 *   实际写到 <html> 的 data-theme 始终是解析后的 light/dark。
 * - 白名单校验：读取值不在白名单即丢弃回退默认（防脏数据/手改注入）。
 * - 切换经 applyWithTransition 启用 0.3s 平滑过渡。
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { applyWithTransition, readStoredValue, writeStoredValue } from './transition';

export type ThemePreference = 'light' | 'dark' | 'system';
export type ResolvedTheme = 'light' | 'dark';

export const THEME_STORAGE_KEY = 'wp-theme';
export const THEME_PREFERENCES: readonly ThemePreference[] = ['light', 'dark', 'system'];

const DARK_MEDIA_QUERY = '(prefers-color-scheme: dark)';

function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system';
}

function readStoredPreference(): ThemePreference {
  const stored = readStoredValue(THEME_STORAGE_KEY);
  return isThemePreference(stored) ? stored : 'system';
}

function systemPrefersDark(): boolean {
  return typeof window !== 'undefined' && !!window.matchMedia?.(DARK_MEDIA_QUERY).matches;
}

interface ThemeContextValue {
  /** 用户偏好（含 system） */
  theme: ThemePreference;
  /** 解析后的实际主题（写入 html[data-theme] 的值） */
  resolvedTheme: ResolvedTheme;
  setTheme: (theme: ThemePreference) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<ThemePreference>(readStoredPreference);
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() =>
    theme === 'system' ? (systemPrefersDark() ? 'dark' : 'light') : theme,
  );

  // 写 <html> 属性（Provider 只负责「读偏好 → 写 html 属性 → 监听系统变化」，不改 CSS）
  useEffect(() => {
    const resolved: ResolvedTheme = theme === 'system' ? (systemPrefersDark() ? 'dark' : 'light') : theme;
    setResolvedTheme(resolved);
    document.documentElement.setAttribute('data-theme', resolved);
  }, [theme]);

  // system 偏好时监听系统明暗变化并动态跟随
  useEffect(() => {
    if (theme !== 'system') return;
    const media = window.matchMedia(DARK_MEDIA_QUERY);
    const onChange = (e: MediaQueryListEvent) => {
      const resolved: ResolvedTheme = e.matches ? 'dark' : 'light';
      setResolvedTheme(resolved);
      document.documentElement.setAttribute('data-theme', resolved);
    };
    media.addEventListener('change', onChange);
    return () => media.removeEventListener('change', onChange);
  }, [theme]);

  const setTheme = useCallback((next: ThemePreference) => {
    if (!isThemePreference(next)) return;
    applyWithTransition(() => {
      writeStoredValue(THEME_STORAGE_KEY, next);
    });
    setThemeState(next);
  }, []);

  const value = useMemo(
    () => ({ theme, resolvedTheme, setTheme }),
    [theme, resolvedTheme, setTheme],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) {
    throw new Error('useTheme 必须在 <ThemeProvider> 内使用');
  }
  return ctx;
}
