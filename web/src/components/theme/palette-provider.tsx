/**
 * 配色 Provider：wp-palette ∈ {p1..p5}，默认 p1。
 * 写 <html data-palette>，纯 CSS 变量换装（tokens.css 4 层选择器结构），零 JS 重绘逻辑。
 * 白名单校验：读取/写入值不在白名单即丢弃回退默认。
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { applyWithTransition, readStoredValue, writeStoredValue } from './transition';

export type Palette = 'p1' | 'p2' | 'p3' | 'p4' | 'p5';

export const PALETTE_STORAGE_KEY = 'wp-palette';
export const PALETTES: readonly Palette[] = ['p1', 'p2', 'p3', 'p4', 'p5'];

export const PALETTE_NAMES: Record<Palette, string> = {
  p1: 'P1 极光紫',
  p2: 'P2 极光蓝',
  p3: 'P3 啄木青绿',
  p4: 'P4 落日琥珀',
  p5: 'P5 幻晶红紫',
};

function isPalette(value: unknown): value is Palette {
  return typeof value === 'string' && (PALETTES as readonly string[]).includes(value);
}

function readStoredPalette(): Palette {
  const stored = readStoredValue(PALETTE_STORAGE_KEY);
  return isPalette(stored) ? stored : 'p1';
}

interface PaletteContextValue {
  palette: Palette;
  setPalette: (palette: Palette) => void;
}

const PaletteContext = createContext<PaletteContextValue | null>(null);

export function PaletteProvider({ children }: { children: ReactNode }) {
  const [palette, setPaletteState] = useState<Palette>(readStoredPalette);

  useEffect(() => {
    document.documentElement.setAttribute('data-palette', palette);
  }, [palette]);

  const setPalette = useCallback((next: Palette) => {
    if (!isPalette(next)) return; // 白名单校验，防脏数据
    applyWithTransition(() => {
      writeStoredValue(PALETTE_STORAGE_KEY, next);
    });
    setPaletteState(next);
  }, []);

  const value = useMemo(() => ({ palette, setPalette }), [palette, setPalette]);

  return <PaletteContext.Provider value={value}>{children}</PaletteContext.Provider>;
}

export function usePalette(): PaletteContextValue {
  const ctx = useContext(PaletteContext);
  if (!ctx) {
    throw new Error('usePalette 必须在 <PaletteProvider> 内使用');
  }
  return ctx;
}
