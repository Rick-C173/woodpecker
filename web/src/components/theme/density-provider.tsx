/**
 * 密度 Provider：消费 theme/density.ts 的持久化 helper，向壳内提供 {density, setDensity}。
 * 写 <html data-density>（全局生效）；管理端在 AdminShell 用局部 data-density='compact'
 * 覆盖（CSS 变量按元素继承，子树覆盖父级），不落盘。
 * 阶段 A 的 /design 演示页直接用 helper，不经此 Provider。
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { applyDensity, restoreDensity, type Density } from './density';

interface DensityContextValue {
  density: Density;
  setDensity: (density: Density) => void;
}

const DensityContext = createContext<DensityContextValue | null>(null);

export function DensityProvider({ children }: { children: ReactNode }) {
  const [density, setDensityState] = useState<Density>('cozy');

  useEffect(() => {
    restoreDensity();
    setDensityState(
      document.documentElement.getAttribute('data-density') === 'compact' ? 'compact' : 'cozy',
    );
  }, []);

  const setDensity = useCallback((next: Density) => {
    applyDensity(next); // 写 localStorage + <html> 属性（带 0.3s 平滑过渡）
    setDensityState(next);
  }, []);

  const value = useMemo(() => ({ density, setDensity }), [density, setDensity]);

  return <DensityContext.Provider value={value}>{children}</DensityContext.Provider>;
}

export function useDensity(): DensityContextValue {
  const ctx = useContext(DensityContext);
  if (!ctx) {
    throw new Error('useDensity 必须在 <DensityProvider> 内使用');
  }
  return ctx;
}
