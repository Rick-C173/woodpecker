/**
 * vitest 全局 setup：
 * - 注册 @testing-library/jest-dom 匹配器扩展（expect(...).toBeInTheDocument() 等）；
 * - ResizeObserver polyfill：radix-ui 浮层组件（DropdownMenu/Select）在 jsdom 下
 *   依赖 ResizeObserver，jsdom 未实现——给组件测试兜底。
 * node 环境的纯逻辑测试不受影响（仅添加匹配器与全局构造器）。
 */
import '@testing-library/jest-dom/vitest';

class ResizeObserverMock {
  callback: ResizeObserverCallback;
  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
  }
  observe(): void {
    /* jsdom 布局恒定，不触发回调 */
  }
  unobserve(): void {}
  disconnect(): void {}
}

if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = ResizeObserverMock as unknown as typeof ResizeObserver;
}
