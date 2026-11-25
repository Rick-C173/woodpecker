/**
 * 主题/配色切换的共享工具：applyWithTransition。
 * 切换瞬间给 <html> 加 .theme-switching 启用 0.3s 颜色过渡，320ms 后移除
 * （避免常驻全局 transition）。供 ThemeProvider / PaletteProvider 复用。
 */

const SWITCHING_CLASS = 'theme-switching';
const SWITCHING_DURATION_MS = 320;

let removeTimer: ReturnType<typeof setTimeout> | null = null;

/** 以 0.3s 平滑过渡执行一次 <html> 属性变更。 */
export function applyWithTransition(apply: () => void): void {
  const root = document.documentElement;
  root.classList.add(SWITCHING_CLASS);
  apply();
  if (removeTimer !== null) {
    clearTimeout(removeTimer);
  }
  removeTimer = setTimeout(() => {
    root.classList.remove(SWITCHING_CLASS);
    removeTimer = null;
  }, SWITCHING_DURATION_MS);
}

/** localStorage 读写，全部 try/catch 包裹（隐私模式静默失败）。 */
export function readStoredValue(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeStoredValue(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* 忽略隐私模式写入失败 */
  }
}
