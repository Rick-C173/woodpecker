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
  openCitationsDrawer: () => void;
  closeCitationsDrawer: () => void;
  toggleCitationsDrawer: () => void;
}

export const useUiStore = create<UiState>((set, get) => ({
  sidebarCollapsed: false,
  toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),

  citationsDrawerOpen: false,
  openCitationsDrawer: () => set({ citationsDrawerOpen: true }),
  closeCitationsDrawer: () => set({ citationsDrawerOpen: false }),
  toggleCitationsDrawer: () => set({ citationsDrawerOpen: !get().citationsDrawerOpen }),
}));
