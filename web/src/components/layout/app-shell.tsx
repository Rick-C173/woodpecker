/**
 * 登录后主布局骨架（本批为骨架，F3 填充真实导航与用户区）：
 * 玻璃拟态侧栏（白名单容器之一）+ 主内容区 Outlet。
 * 侧栏底部用户区：主题/配色切换入口（架构 §5.4 第 7 条）。
 */

import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { FolderKanban, ShieldCheck } from 'lucide-react';
import { ThemeControls } from '@/components/theme/theme-controls';
import { useAuth } from '@/hooks/use-auth';

export function AppShell() {
  const { user } = useAuth();
  const { pathname } = useLocation();

  return (
    <div className="relative z-[1] flex h-screen">
      <aside className="sidebar flex w-56 flex-none flex-col px-2.5 py-4">
        <div className="flex items-center gap-2 px-2 pb-4 font-bold">
          <span className="flex size-8 items-center justify-center rounded-[var(--radius)] bg-[var(--grad)] text-white shadow-[var(--btn-shadow)]">
            🐦
          </span>
          啄木鸟 Woodpecker
        </div>

        <nav className="mt-3 flex flex-1 flex-col gap-1">
          <NavLink to="/kbs" className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}>
            <FolderKanban aria-hidden="true" />
            知识库
          </NavLink>
          {user?.is_admin ? (
            <NavLink
              to="/admin/users"
              className={({ isActive }) =>
                `nav-item${isActive || pathname.startsWith('/admin') ? ' active' : ''}`
              }
            >
              <ShieldCheck aria-hidden="true" />
              管理后台
            </NavLink>
          ) : null}
        </nav>

        <div className="border-t border-[var(--border)] pt-3">
          <div className="flex items-center gap-2.5 px-2 pb-3">
            <span className="avatar-sm">{user?.username?.charAt(0)?.toUpperCase() ?? '?'}</span>
            <div className="min-w-0">
              <div className="truncate text-[13px] font-semibold leading-tight">
                {user?.username ?? '未登录'}
              </div>
              <div className="text-[11px] text-[var(--muted)]">
                {user?.is_admin ? '系统管理员' : '普通用户'}
              </div>
            </div>
          </div>
          <ThemeControls />
        </div>
      </aside>

      <main className="min-w-0 flex-1 overflow-auto">
        <Outlet />
      </main>
    </div>
  );
}
