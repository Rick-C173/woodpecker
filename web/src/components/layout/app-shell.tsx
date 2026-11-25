/**
 * 登录后主布局（F3 填充）：玻璃拟态侧栏（白名单容器）+ 主内容区 Outlet。
 * 侧栏：知识库导航列表（紧凑，取 /kbs 前 N 个）+ 管理员专属「管理后台」入口；
 * 底部：主题/配色切换（架构 §5.4 第 7 条）+ 当前用户 + 退出登录。
 */

import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { FolderKanban, LogOut, ShieldCheck } from 'lucide-react';
import { toast } from 'sonner';
import { ThemeControls } from '@/components/theme/theme-controls';
import { Button } from '@/components/ui/button';
import { useAuth } from '@/hooks/use-auth';
import { useKbsList } from '@/hooks/use-kbs';
import { isLoggedIn } from '@/lib/auth';

/** 侧栏知识库导航最多展示数（更多走 /kbs 列表页）。 */
const SIDEBAR_KB_LIMIT = 8;

export function AppShell() {
  const { user, logout } = useAuth();
  const { pathname } = useLocation();
  const navigate = useNavigate();

  // 未登录（守卫跳转瞬间）不发列表请求，避免 401 噪音
  const kbs = useKbsList(isLoggedIn());
  const sidebarKbs = (kbs.data ?? []).slice(0, SIDEBAR_KB_LIMIT);

  const handleLogout = () => {
    logout();
    toast.success('已退出登录');
    navigate('/login', { replace: true });
  };

  return (
    <div className="relative z-[1] flex h-screen">
      <aside className="sidebar flex w-60 flex-none flex-col px-2.5 py-4">
        <div className="flex items-center gap-2 px-2 pb-4 font-bold">
          <span className="flex size-8 items-center justify-center rounded-[var(--radius)] bg-[var(--grad)] text-white shadow-[var(--btn-shadow)]">
            🐦
          </span>
          啄木鸟 Woodpecker
        </div>

        <nav className="mt-1 flex flex-1 flex-col gap-1 overflow-y-auto">
          <NavLink to="/kbs" className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}>
            <FolderKanban aria-hidden="true" />
            全部知识库
          </NavLink>

          {/* 知识库导航列表（紧凑） */}
          {kbs.isLoading ? (
            <p className="px-2 py-1 text-xs text-[var(--faint)]">加载中…</p>
          ) : (
            sidebarKbs.map((kb) => (
              <NavLink
                key={kb.id}
                to={`/kbs/${kb.id}`}
                className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}
                title={kb.name ?? ''}
              >
                <span
                  className="size-1.5 flex-none rounded-full bg-[var(--primary)] opacity-70"
                  aria-hidden="true"
                />
                <span className="truncate">{kb.name}</span>
              </NavLink>
            ))
          )}
          {(kbs.data?.length ?? 0) > SIDEBAR_KB_LIMIT ? (
            <p className="px-2 py-1 text-xs text-[var(--faint)]">
              … 共 {kbs.data?.length} 个，更多见列表页
            </p>
          ) : null}

          {/* 管理员专属分区入口 */}
          {user?.is_admin ? (
            <NavLink
              to="/admin/users"
              className={({ isActive }) =>
                `nav-item mt-2${isActive || pathname.startsWith('/admin') ? ' active' : ''}`
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
          <div className="px-2">
            <ThemeControls />
          </div>
          <Button
            variant="ghost"
            size="sm"
            className="mt-2 w-full justify-start text-[var(--muted)]"
            onClick={handleLogout}
          >
            <LogOut aria-hidden="true" />
            退出登录
          </Button>
        </div>
      </aside>

      <main className="min-w-0 flex-1 overflow-auto">
        <Outlet />
      </main>
    </div>
  );
}
