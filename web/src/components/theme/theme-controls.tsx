/**
 * 主题/配色切换入口（架构 §5.4 第 7 条）：
 * 主题按钮（日/月图标，含 system 三态）+ 配色圆点组（渐变填充 + 名称 tooltip）。
 * 位置：侧栏底部用户区 + 登录页卡片顶部（demo 顶栏布局不照搬）。
 */

import { Moon, Sun, SunMoon } from 'lucide-react';
import { useTheme, type ThemePreference } from './theme-provider';
import { PALETTES, PALETTE_NAMES, usePalette } from './palette-provider';

export function ThemeControls() {
  const { theme, resolvedTheme, setTheme } = useTheme();
  const { palette, setPalette } = usePalette();

  const toggleTheme = () => {
    // 三态循环：system → light → dark → system
    const next: ThemePreference =
      theme === 'system' ? 'light' : theme === 'light' ? 'dark' : 'system';
    setTheme(next);
  };

  return (
    <div className="flex items-center justify-between gap-2 w-full">
      <div className="palette-dots" role="group" aria-label="配色方案切换">
        {PALETTES.map((p) => (
          <button
            key={p}
            type="button"
            className={`p-dot ${p}${palette === p ? ' active' : ''}`}
            data-name={PALETTE_NAMES[p]}
            aria-label={`配色方案：${PALETTE_NAMES[p]}`}
            title={PALETTE_NAMES[p]}
            onClick={() => setPalette(p)}
          />
        ))}
      </div>
      <button
        type="button"
        className="theme-toggle"
        title={`明暗主题：${theme === 'system' ? '跟随系统' : theme === 'light' ? '浅色' : '深色'}`}
        aria-label="切换明暗主题"
        onClick={toggleTheme}
      >
        {theme === 'system' ? (
          <SunMoon aria-hidden="true" />
        ) : resolvedTheme === 'dark' ? (
          <Sun aria-hidden="true" />
        ) : (
          <Moon aria-hidden="true" />
        )}
      </button>
    </div>
  );
}
