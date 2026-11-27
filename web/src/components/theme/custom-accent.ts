/**
 * 自定义强调色：hex → HSL，写 <html> 内联 --acc-h/s/l。
 * 预设色板（data-palette）与自定义共用同一条 tokens.css 派生链——
 * 内联样式优先级高于一切选择器，故「选预设」时需 clear() 移除内联值。
 * 持久化：localStorage wp-custom-accent（hex），null 表示未自定义。
 */

import { applyWithTransition, readStoredValue, writeStoredValue } from './transition';

export const CUSTOM_ACCENT_STORAGE_KEY = 'wp-custom-accent';

/** 读已保存的自定义色（hex），未设置返回 null。 */
export function readCustomAccent(): string | null {
  return readStoredValue(CUSTOM_ACCENT_STORAGE_KEY);
}

/** hex(#rgb/#rrggbb) → HSL（h∈[0,360) 整数，s/l∈[0,100] 整数百分比）。 */
export function hexToHSL(hex: string): { h: number; s: number; l: number } | null {
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return null;
  let value = m[1];
  if (value.length === 3) {
    value = value
      .split('')
      .map((c) => c + c)
      .join('');
  }
  const r = parseInt(value.slice(0, 2), 16) / 255;
  const g = parseInt(value.slice(2, 4), 16) / 255;
  const b = parseInt(value.slice(4, 6), 16) / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  let h = 0;
  let s = 0;
  if (max !== min) {
    const d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    switch (max) {
      case r:
        h = ((g - b) / d + (g < b ? 6 : 0)) * 60;
        break;
      case g:
        h = ((b - r) / d + 2) * 60;
        break;
      default:
        h = ((r - g) / d + 4) * 60;
    }
  }
  return { h: Math.round(h) % 360, s: Math.round(s * 100), l: Math.round(l * 100) };
}

/**
 * 应用自定义强调色：写 localStorage + <html> 内联 --acc-h/s/l。
 * 非法 hex 直接拒绝（返回 false），不产生任何副作用。
 */
export function applyCustomAccent(hex: string): boolean {
  const hsl = hexToHSL(hex);
  if (!hsl) return false;
  applyWithTransition(() => {
    writeStoredValue(CUSTOM_ACCENT_STORAGE_KEY, hex.trim().toLowerCase());
    document.documentElement.style.setProperty('--acc-h', String(hsl.h));
    document.documentElement.style.setProperty('--acc-s', hsl.s + '%');
    document.documentElement.style.setProperty('--acc-l', hsl.l + '%');
  });
  return true;
}

/** 清除自定义（恢复 data-palette 预设）：删 localStorage + 移除内联变量。 */
export function clearCustomAccent(): void {
  applyWithTransition(() => {
    writeStoredValue(CUSTOM_ACCENT_STORAGE_KEY, '');
    document.documentElement.style.removeProperty('--acc-h');
    document.documentElement.style.removeProperty('--acc-s');
    document.documentElement.style.removeProperty('--acc-l');
  });
}

/** 启动时恢复：localStorage 有自定义色则回放内联变量（/design 与后续用户设置共用）。 */
export function restoreCustomAccent(): void {
  const stored = readCustomAccent();
  if (!stored) return;
  const hsl = hexToHSL(stored);
  if (!hsl) return;
  document.documentElement.style.setProperty('--acc-h', String(hsl.h));
  document.documentElement.style.setProperty('--acc-s', hsl.s + '%');
  document.documentElement.style.setProperty('--acc-l', hsl.l + '%');
}
