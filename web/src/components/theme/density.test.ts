// @vitest-environment jsdom
/** 密度 helper：白名单读取 / 写 localStorage + <html data-density> / 脏数据回退。 */

import { afterEach, describe, expect, it } from 'vitest';
import { applyDensity, densityLabel, DENSITY_STORAGE_KEY, readStoredDensity, restoreDensity } from './density';

describe('density helpers', () => {
  afterEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute('data-density');
  });

  it('无存储值 → 默认 cozy', () => {
    expect(readStoredDensity()).toBe('cozy');
  });

  it('合法存储值 → 原样返回', () => {
    localStorage.setItem(DENSITY_STORAGE_KEY, 'compact');
    expect(readStoredDensity()).toBe('compact');
  });

  it('脏数据（白名单外）→ 回退 cozy', () => {
    localStorage.setItem(DENSITY_STORAGE_KEY, 'mega-dense');
    expect(readStoredDensity()).toBe('cozy');
  });

  it('applyDensity：写 localStorage + <html> 属性', () => {
    applyDensity('compact');
    expect(localStorage.getItem(DENSITY_STORAGE_KEY)).toBe('compact');
    expect(document.documentElement.getAttribute('data-density')).toBe('compact');
  });

  it('restoreDensity：无存储值时归位 cozy（保证 CSS 与存储一致）', () => {
    document.documentElement.setAttribute('data-density', 'compact');
    restoreDensity();
    expect(document.documentElement.getAttribute('data-density')).toBe('cozy');
  });

  it('densityLabel：中文文案', () => {
    expect(densityLabel('cozy')).toBe('舒适');
    expect(densityLabel('compact')).toBe('紧凑');
  });
});
