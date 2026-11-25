/**
 * uiStore：纯 UI 态（架构 §1.7）。任何服务端数据不进此 store。
 * 主题偏好不在此处（由 ThemeProvider/PaletteProvider 以 wp-theme/wp-palette 键持久化）。
 */

import { create } from 'zustand';

interface UiState {
  /** 知识库侧栏折叠 */
  sidebarCollapsed: boolean;
  toggleSidebar: () => void;
  /** 引用抽屉开关（聊天窗内） */
  citationsDrawerOpen: boolean;
  /** 抽屉当前聚焦的引用 seq（点击行内 [n] / 引用 chip 联动滚动） */
  citationsSeq: number | null;
  /** 打开抽屉并聚焦某条引用 */
  openCitation: (seq: number) => void;
  closeCitationsDrawer: () => void;
}

export const useUiStore = create<UiState>((set) => ({
  sidebarCollapsed: false,
  toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),

  citationsDrawerOpen: false,
  citationsSeq: null,
  openCitation: (seq) => set({ citationsDrawerOpen: true, citationsSeq: seq }),
  closeCitationsDrawer: () => set({ citationsDrawerOpen: false, citationsSeq: null }),
}));
