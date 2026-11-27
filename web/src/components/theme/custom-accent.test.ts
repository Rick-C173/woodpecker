// @vitest-environment jsdom
/** 自定义强调色 helper：hexToHSL 派生正确性 + 内联变量/持久化的应用与清除。 */

import { afterEach, describe, expect, it } from 'vitest';
import {
  applyCustomAccent,
  clearCustomAccent,
  CUSTOM_ACCENT_STORAGE_KEY,
  hexToHSL,
  readCustomAccent,
  restoreCustomAccent,
} from './custom-accent';

describe('hexToHSL', () => {
  it('极值：#000 → 黑；#fff → 白', () => {
    expect(hexToHSL('#000000')).toEqual({ h: 0, s: 0, l: 0 });
    expect(hexToHSL('#ffffff')).toEqual({ h: 0, s: 0, l: 100 });
  });

  it('纯红 #f00（3 位展开）→ hsl(0,100%,50%)', () => {
    expect(hexToHSL('#f00')).toEqual({ h: 0, s: 100, l: 50 });
  });

  it('大小写与 # 前缀容错：#FF6B35 与 #ff6b35 同值', () => {
    const upper = hexToHSL('#FF6B35');
    const lower = hexToHSL('#ff6b35');
    expect(upper).toEqual(lower);
    expect(upper).not.toBeNull();
    expect(upper!.s).toBeGreaterThan(50); // 橙色系高饱和
    expect(upper!.l).toBeGreaterThan(40);
  });

  it('非法输入 → null（颜色、乱码、超长）', () => {
    expect(hexToHSL('e88b40')).toBeNull(); // 缺 #
    expect(hexToHSL('#12g45z')).toBeNull();
    expect(hexToHSL('#12345')).toBeNull();
    expect(hexToHSL('')).toBeNull();
  });
});

describe('applyCustomAccent / clearCustomAccent / restoreCustomAccent', () => {
  afterEach(() => {
    localStorage.clear();
    document.documentElement.style.removeProperty('--acc-h');
    document.documentElement.style.removeProperty('--acc-s');
    document.documentElement.style.removeProperty('--acc-l');
  });

  it('合法色：写内联 --acc-h/s/l + localStorage，readCustomAccent 回读', () => {
    expect(applyCustomAccent('#ff6b35')).toBe(true);
    const root = document.documentElement.style;
    expect(root.getPropertyValue('--acc-h')).toBe('16'); // #ff6b35 ≈ hsl(16,100%,60%)
    expect(root.getPropertyValue('--acc-s')).toBe('100%');
    expect(readCustomAccent()).toBe('#ff6b35');
  });

  it('非法色：直接拒绝，不产生任何副作用', () => {
    expect(applyCustomAccent('not-a-color')).toBe(false);
    expect(readCustomAccent()).toBeNull();
    expect(document.documentElement.style.getPropertyValue('--acc-h')).toBe('');
  });

  it('clear：移除内联变量，存储置空（falsy = 未自定义）', () => {
    applyCustomAccent('#ff6b35');
    clearCustomAccent();
    expect(document.documentElement.style.getPropertyValue('--acc-h')).toBe('');
    expect(readCustomAccent()).toBeFalsy();
  });

  it('restore：localStorage 有值时回放内联变量（刷新持久化链路）', () => {
    localStorage.setItem(CUSTOM_ACCENT_STORAGE_KEY, '#3478f6');
    restoreCustomAccent();
    expect(document.documentElement.style.getPropertyValue('--acc-h')).not.toBe('');
    expect(document.documentElement.style.getPropertyValue('--acc-s')).toMatch(/%$/);
  });

  it('restore：存储脏数据时静默忽略', () => {
    localStorage.setItem(CUSTOM_ACCENT_STORAGE_KEY, '#xyz');
    restoreCustomAccent();
    expect(document.documentElement.style.getPropertyValue('--acc-h')).toBe('');
  });
});
