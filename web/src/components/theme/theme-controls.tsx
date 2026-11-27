/**
 * 主题/配色控件（架构 §5.4 第 7 条）：
 * - ThemeToggleButton：单按钮三态循环（跟随系统 -> 浅色 -> 深色），侧栏底部用；
 * - PaletteDots：配色圆点组（渐变填充 + 名称 tooltip）；
 * - ThemeSettingsSection：完整外观设置区块（主题模式按钮组 + 配色圆点），系统设置页用。
 * 登录页不再放任何外观控件（2026-11-26 用户反馈）。
 */
import { Moon, Sun, SunMoon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { PALETTES, PALETTE_NAMES, usePalette } from './palette-provider';
import { clearCustomAccent } from './custom-accent';
import { useTheme, THEME_PREFERENCES, type ThemePreference } from './theme-provider';

const THEME_META: Record<ThemePreference, { label: string }> = {
  system: { label: '跟随系统' },
  light: { label: '浅色' },
  dark: { label: '深色' },
};

export function ThemeToggleButton() {
  const { theme, resolvedTheme, setTheme } = useTheme();
  const toggleTheme = () => {
    const next: ThemePreference =
      theme === 'system' ? 'light' : theme === 'light' ? 'dark' : 'system';
    setTheme(next);
  };
  return (
    <button
      type="button"
      className="theme-toggle"
      title={'明暗主题：' + THEME_META[theme].label}
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
  );
}

export function PaletteDots({ className }: { className?: string }) {
  const { palette, setPalette } = usePalette();
  return (
    <div className={cn('palette-dots', className)} role="group" aria-label="配色方案切换">
      {PALETTES.map((p) => (
        <button
          key={p}
          type="button"
          className={'p-dot ' + p + (palette === p ? ' active' : '')}
          data-name={PALETTE_NAMES[p]}
          aria-label={'配色方案：' + PALETTE_NAMES[p]}
          title={PALETTE_NAMES[p]}
          onClick={() => {
            // 内联 --acc-h/s/l（自定义色）优先级高于预设，切预设前必须清除
            clearCustomAccent();
            setPalette(p);
          }}
        />
      ))}
    </div>
  );
}

export function ThemeSettingsSection() {
  const { theme, setTheme } = useTheme();
  return (
    <div className="space-y-4" data-testid="theme-settings">
      <div>
        <p className="mb-2 text-sm font-medium text-[var(--foreground)]">主题模式</p>
        <div className="flex gap-2" role="group" aria-label="明暗主题模式">
          {THEME_PREFERENCES.map((p) => (
            <button
              key={p}
              type="button"
              onClick={() => setTheme(p)}
              aria-pressed={theme === p}
              className={cn(
                'inline-flex items-center gap-1.5 rounded-[var(--radius)] border px-3 py-1.5 text-sm transition-colors',
                theme === p
                  ? 'border-[var(--border-strong)] bg-[var(--primary-soft)] font-medium text-[var(--primary)]'
                  : 'border-[var(--border)] text-[var(--muted)] hover:border-[var(--border-strong)] hover:text-[var(--foreground)]',
              )}
            >
              {p === 'system' ? <SunMoon className="size-4" aria-hidden="true" /> : p === 'light' ? <Sun className="size-4" aria-hidden="true" /> : <Moon className="size-4" aria-hidden="true" />}
              {THEME_META[p].label}
            </button>
          ))}
        </div>
        <p className="mt-1.5 text-xs text-[var(--faint)]">
          {theme === 'system'
            ? '跟随操作系统明暗设置自动切换。'
            : theme === 'light'
              ? '始终使用浅色主题。'
              : '始终使用深色主题。'}
        </p>
      </div>
      <div>
        <p className="mb-2 text-sm font-medium text-[var(--foreground)]">配色方案</p>
        <PaletteDots />
        <p className="mt-1.5 text-xs text-[var(--faint)]">预设强调色即时生效，仅影响当前浏览器。</p>
      </div>
    </div>
  );
}
