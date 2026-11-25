/**
 * use-debounced-value：防抖 hook（检索页 300ms 防抖 + q 为空不发请求）。
 */

import { useEffect, useState } from 'react';

/** 返回 value 的防抖副本；value 停止变化 delay ms 后更新。 */
export function useDebouncedValue<T>(value: T, delay = 300): T {
  const [debounced, setDebounced] = useState<T>(value);

  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);

  return debounced;
}
