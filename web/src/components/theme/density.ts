/**
 * 密度档：wp-density ∈ {cozy, compact}，默认 cozy。
 * 写 <html data-density>（tokens.css 密度双档选择器消费）。
 * 阶段 A 由 /design 演示页使用；阶段 B 由 Shell Provider 正式接管同一套 helper。
 */

import { applyWithTransition, readStoredValue, writeStoredValue } from './transition';

export type Density = 'cozy' | 'compact';
export const DENSITY_STORAGE_KEY = 'wp-density';
export const DENSITIES: readonly Density[] = ['cozy', 'compact'];

const DENSITY_META: Record<Density, string> = {
  cozy: '舒适',
  compact: '紧凑',
};

function isDensity(value: unknown): value is Density {
  return value === 'cozy' || value === 'compact';
}

/** 读密度偏好（白名单校验，脏数据回退 cozy）。 */
export function readStoredDensity(): Density {
  const stored = readStoredValue(DENSITY_STORAGE_KEY);
  return isDensity(stored) ? stored : 'cozy';
}

export function densityLabel(d: Density): string {
  return DENSITY_META[d];
}

/** 写 localStorage + <html data-density>，带 0.3s 平滑过渡。 */
export function applyDensity(density: Density): void {
  if (!isDensity(density)) return;
  applyWithTransition(() => {
    writeStoredValue(DENSITY_STORAGE_KEY, density);
    document.documentElement.setAttribute('data-density', density);
  });
}

/** 启动时恢复（无存储值时归位 cozy，保证 CSS 与存储一致）。 */
export function restoreDensity(): void {
  document.documentElement.setAttribute('data-density', readStoredDensity());
}
