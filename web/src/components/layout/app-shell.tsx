/**
 * 登录后主布局（v2 冷静工具风，规划 §4：一套框架，两种模式）：
 * - 侧栏变体按路由切换：用户侧（知识库 / 最近访问 / 个人）↔ /admin/*（管理侧三组导航），
 *   双栏不嵌套；折叠动画为 demo 批准模式——宽度过渡 + 文字 max-width 收起，
 *   图标位置两态恒定（padding/图标/行高不变），无跳动。
 * - 顶栏：命令面板入口（Ctrl+K）+ 面包屑（深度 ≥2 且有跳转意义才展示）+ 明暗切换。
 * - 侧栏底部用户菜单：修改密码 / 外观（明暗·配色·密度）/ 退出登录。
 * - 管理端密度：AdminShell 内局部 data-density='compact' 覆盖，不落盘。
 */

import { Fragment } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import {
  ArrowLeft,
  Bird,
  BookMarked,
  ChevronUp,
  KeyRound,
  LibraryBig,
  LogOut,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  ShieldCheck,
} from 'lucide-react';
import { toast } from 'sonner';
import { useUiStore } from '@/stores/ui-store';
import { useAuth } from '@/hooks/use-auth';
import { useKbsList } from '@/hooks/use-kbs';
import { isLoggedIn } from '@/lib/auth';
import { useTheme, THEME_PREFERENCES } from '@/components/theme/theme-provider';
import { ThemeToggleButton, PaletteDots } from '@/components/theme/theme-controls';
import { DensityProvider, useDensity } from '@/components/theme/density-provider';
import { DENSITIES, densityLabel } from '@/components/theme/density';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb';
import { ADMIN_NAV_GROUPS, ADMIN_LABELS } from './admin-shell';
import { cn } from '@/lib/utils';

/** 侧栏知识库导航最多展示数（更多走 /kbs 列表页）。 */
const SIDEBAR_KB_LIMIT = 8;

const COLLAPSE_SPAN =
  'overflow-hidden whitespace-nowrap transition-[max-width,opacity] duration-200 ease-[cubic-bezier(0.2,0,0,1)]';

/** 可折叠文字 span：max-width 200px ↔ 0（demo 批准模式，禁用省略号）。 */
function CollapsibleSpan({
  collapsed,
  className,
  children,
}: {
  collapsed: boolean;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <span className={cn(COLLAPSE_SPAN, collapsed ? 'max-w-0 opacity-0' : 'max-w-[200px] opacity-100', className)}>
      {children}
    </span>
  );
}

/** 侧栏导航行：行高/内距/图标位两态恒定，激活态 = accent 弱底 + 强调色文字。 */
function NavRow({
  to,
  icon: Icon,
  label,
  collapsed,
  end,
}: {
  to: string;
  icon: React.ComponentType<{ className?: string; 'aria-hidden'?: boolean | 'true' | 'false' }>;
  label: string;
  collapsed: boolean;
  end?: boolean;
}) {
  return (
    <NavLink
      to={to}
      end={end}
      title={label}
      className={({ isActive }) =>
        cn(
          'flex h-[34px] items-center gap-2.5 rounded-[var(--radius-sm)] px-2.5 text-[13px] transition-colors',
          isActive
            ? 'bg-[var(--accent-weak)] font-semibold text-[var(--accent)]'
            : 'text-[var(--muted)] hover:bg-[var(--hover-bg)] hover:text-[var(--foreground)]',
        )
      }
    >
      <Icon className="size-4 shrink-0" aria-hidden={true} />
      <CollapsibleSpan collapsed={collapsed}>{label}</CollapsibleSpan>
    </NavLink>
  );
}

/** 分组标题：折叠时文字收起、分割线淡入（两态高度/内距恒定）。 */
function NavGroup({ label, collapsed }: { label: string; collapsed: boolean }) {
  return (
    <div className="flex items-center gap-2 overflow-hidden px-2.5 pb-1 pt-4">
      <CollapsibleSpan
        collapsed={collapsed}
        className="text-[11px] font-semibold uppercase tracking-wider text-[var(--faint)]"
      >
        {label}
      </CollapsibleSpan>
      <span
        aria-hidden
        className={cn(
          'h-px flex-1 bg-[var(--border)] transition-opacity duration-200',
          collapsed ? 'opacity-100' : 'opacity-0',
        )}
      />
    </div>
  );
}

/** 面包屑模型：深度 ≥2 且有跳转意义才返回非 null（规划 §2.2）。 */
function breadcrumbsFor(
  pathname: string,
): Array<{ label: string; to?: string }> | null {
  let m = /^\/kbs\/([^/]+)\/documents\/([^/]+)/.exec(pathname);
  if (m) return [{ label: '知识库', to: '/kbs' }, { label: '文档' }];
  m = /^\/kbs\/([^/]+)\/search/.exec(pathname);
  if (m) return [{ label: '知识库', to: '/kbs' }, { label: '搜索' }];
  m = /^\/kbs\/([^/]+)\/chat/.exec(pathname);
  if (m) return [{ label: '知识库', to: '/kbs' }, { label: '问答' }];
  m = /^\/kbs\/([^/]+)/.exec(pathname);
  if (m) return [{ label: '知识库', to: '/kbs' }, { label: '知识库详情' }];
  if (pathname.startsWith('/admin')) {
    const label = ADMIN_LABELS[pathname];
    return label ? [{ label: '管理后台', to: '/admin/users' }, { label }] : [{ label: '管理后台' }];
  }
  return null;
}

/** 外观设置小节（用户菜单内）：明暗三态 · 配色五点 · 密度双档。 */
function AppearanceSection() {
  const { theme, setTheme } = useTheme();
  const { density, setDensity } = useDensity();
  const segBtn = (active: boolean) =>
    cn(
      'h-7 rounded-[var(--radius-sm)] px-2 text-xs transition-colors',
      active
        ? 'bg-[var(--accent)] font-medium text-[var(--on-accent)]'
        : 'text-[var(--muted)] hover:bg-[var(--hover-bg)] hover:text-[var(--foreground)]',
    );
  return (
    <div className="space-y-2 px-2 pb-2">
      <div className="flex items-center gap-1">
        <span className="w-8 shrink-0 text-xs text-[var(--faint)]">明暗</span>
        {THEME_PREFERENCES.map((p) => (
          <button key={p} type="button" className={segBtn(theme === p)} aria-pressed={theme === p} onClick={() => setTheme(p)}>
            {p === 'system' ? '跟随系统' : p === 'light' ? '浅色' : '深色'}
          </button>
        ))}
      </div>
      <div className="flex items-center gap-3">
        <span className="w-8 shrink-0 text-xs text-[var(--faint)]">配色</span>
        <PaletteDots />
      </div>
      <div className="flex items-center gap-1">
        <span className="w-8 shrink-0 text-xs text-[var(--faint)]">密度</span>
        {DENSITIES.map((d) => (
          <button key={d} type="button" className={segBtn(density === d)} aria-pressed={density === d} onClick={() => setDensity(d)}>
            {densityLabel(d)}
          </button>
        ))}
      </div>
    </div>
  );
}

function ShellInner() {
  const { user, logout } = useAuth();
  const collapsed = useUiStore((s) => s.sidebarCollapsed);
  const toggleSidebar = useUiStore((s) => s.toggleSidebar);
  const setCommandOpen = useUiStore((s) => s.setCommandOpen);
  const { pathname } = useLocation();
  const navigate = useNavigate();

  const isAdminRoute = pathname.startsWith('/admin');
  const breadcrumbs = breadcrumbsFor(pathname);

  // 未登录（守卫跳转瞬间）不发列表请求，避免 401 噪音
  const kbs = useKbsList(!isAdminRoute && isLoggedIn());
  const sidebarKbs = (kbs.data ?? []).slice(0, SIDEBAR_KB_LIMIT);

  const handleLogout = () => {
    logout();
    toast.success('已退出登录');
    navigate('/login', { replace: true });
  };

  return (
    <div className="flex h-screen bg-[var(--background)] text-[var(--foreground)]">
      {/* ---------- 侧栏（双变体） ---------- */}
      <aside
        className={cn(
          'flex h-full flex-none flex-col border-r border-[var(--border)] bg-[var(--card)]',
          'transition-[width] duration-200 ease-[cubic-bezier(0.2,0,0,1)]',
          collapsed ? 'w-14' : 'w-[var(--side-w)]',
        )}
      >
        {/* 品牌 */}
        <div className="flex h-12 shrink-0 items-center gap-2 px-2.5">
          <span
            className="flex size-7 shrink-0 items-center justify-center rounded-[var(--radius-sm)] bg-[var(--accent)] text-[var(--on-accent)]"
            aria-hidden
          >
            <Bird className="size-4" />
          </span>
          <CollapsibleSpan collapsed={collapsed} className="text-sm font-bold">
            啄木鸟
          </CollapsibleSpan>
        </div>
        {/* 折叠开关：位置两态恒定（左对齐），仅图标语义切换 */}
        <div className="flex h-8 shrink-0 items-center px-2.5 pb-1">
          <button
            type="button"
            onClick={toggleSidebar}
            title={collapsed ? '展开侧栏' : '折叠侧栏'}
            aria-label={collapsed ? '展开侧栏' : '折叠侧栏'}
            aria-expanded={!collapsed}
            data-testid="sidebar-toggle"
            className="flex size-7 items-center justify-center rounded-[var(--radius-sm)] text-[var(--muted)] transition-colors hover:bg-[var(--hover-bg)] hover:text-[var(--foreground)]"
          >
            {collapsed ? <PanelLeftOpen className="size-4" /> : <PanelLeftClose className="size-4" />}
          </button>
        </div>

        {/* 导航区 */}
        <nav className="min-h-0 flex-1 overflow-y-auto px-2.5 pb-2">
          {isAdminRoute ? (
            <>
              <NavRow to="/kbs" icon={ArrowLeft} label="返回知识库" collapsed={collapsed} />
              {ADMIN_NAV_GROUPS.map((group) => (
                <div key={group.label}>
                  <NavGroup label={group.label} collapsed={collapsed} />
                  {group.items.map((item) => (
                    <NavRow key={item.to} to={item.to} icon={item.icon} label={item.label} collapsed={collapsed} />
                  ))}
                </div>
              ))}
            </>
          ) : (
            <>
              <NavRow to="/kbs" icon={LibraryBig} label="知识库" collapsed={collapsed} end />
              {sidebarKbs.length > 0 && (
                <>
                  <NavGroup label="最近访问" collapsed={collapsed} />
                  {kbs.isLoading ? (
                    <p className="px-2.5 py-1 text-xs text-[var(--faint)]">加载中…</p>
                  ) : (
                    sidebarKbs.map((kb) => (
                      <NavRow
                        key={kb.id}
                        to={`/kbs/${kb.id}`}
                        icon={BookMarked}
                        label={kb.name ?? ''}
                        collapsed={collapsed}
                      />
                    ))
                  )}
                </>
              )}
              <NavGroup label="个人" collapsed={collapsed} />
              <NavRow to="/change-password" icon={KeyRound} label="修改密码" collapsed={collapsed} />
              {user?.is_admin && (
                <>
                  <NavGroup label="管理" collapsed={collapsed} />
                  <NavRow to="/admin/users" icon={ShieldCheck} label="管理后台" collapsed={collapsed} />
                </>
              )}
            </>
          )}
        </nav>

        {/* 用户菜单 */}
        <div className="shrink-0 border-t border-[var(--border)] p-2.5">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                data-testid="user-menu-trigger"
                aria-label="用户菜单"
                title={user?.username ?? ''}
                className="flex h-11 w-full items-center gap-2.5 rounded-[var(--radius-sm)] px-1.5 transition-colors hover:bg-[var(--hover-bg)]"
              >
                <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-[var(--accent)] text-[13px] font-semibold text-[var(--on-accent)]">
                  {user?.username?.charAt(0)?.toUpperCase() ?? '?'}
                </span>
                <span className="min-w-0 flex-1 text-left">
                  <CollapsibleSpan collapsed={collapsed} className="block text-[13px] font-semibold leading-tight">
                    {user?.username ?? '未登录'}
                  </CollapsibleSpan>
                  <CollapsibleSpan collapsed={collapsed} className="block text-[11px] leading-tight text-[var(--muted)]">
                    {user?.is_admin ? '系统管理员' : '普通用户'}
                  </CollapsibleSpan>
                </span>
                <ChevronUp
                  className={cn('size-3.5 shrink-0 text-[var(--faint)] transition-opacity duration-200', collapsed ? 'opacity-0' : 'opacity-100')}
                  aria-hidden
                />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent side="top" align="start" className="w-64">
              <DropdownMenuLabel>
                {user?.username ?? '未登录'} · {user?.is_admin ? '系统管理员' : '普通用户'}
              </DropdownMenuLabel>
              <DropdownMenuItem onSelect={() => navigate('/change-password')}>
                <KeyRound aria-hidden />
                修改密码
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuLabel>外观</DropdownMenuLabel>
              <AppearanceSection />
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive" onSelect={handleLogout}>
                <LogOut aria-hidden />
                退出登录
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </aside>

      {/* ---------- 右列：顶栏 + 内容 ---------- */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-3 border-b border-[var(--border)] bg-[var(--card)] px-4">
          <button
            type="button"
            onClick={() => setCommandOpen(true)}
            data-testid="command-palette-trigger"
            aria-label="打开命令面板（Ctrl+K）"
            className="flex h-8 w-56 items-center gap-2 rounded-[var(--radius-sm)] border border-[var(--border)] px-2.5 text-[13px] text-[var(--faint)] transition-colors hover:border-[var(--border-strong)] hover:text-[var(--muted)]"
          >
            <Search className="size-3.5" aria-hidden />
            <span className="flex-1 text-left">搜索或跳转…</span>
            <kbd className="rounded border border-[var(--border)] px-1 text-[10px]">Ctrl K</kbd>
          </button>
          {breadcrumbs && (
            <Breadcrumb>
              <BreadcrumbList>
                {breadcrumbs.map((crumb, i) => {
                  const last = i === breadcrumbs.length - 1;
                  return (
                    <Fragment key={crumb.label}>
                      <BreadcrumbItem>
                        {last ? (
                          <BreadcrumbPage>{crumb.label}</BreadcrumbPage>
                        ) : (
                          <NavLink
                            to={crumb.to ?? '#'}
                            className="transition-colors hover:text-[var(--foreground)] hover:underline"
                          >
                            {crumb.label}
                          </NavLink>
                        )}
                      </BreadcrumbItem>
                      {!last && <BreadcrumbSeparator />}
                    </Fragment>
                  );
                })}
              </BreadcrumbList>
            </Breadcrumb>
          )}
          <div className="ml-auto flex items-center">
            <ThemeToggleButton />
          </div>
        </header>
        <main className="min-w-0 flex-1 overflow-auto">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

export function AppShell() {
  return (
    <DensityProvider>
      <ShellInner />
    </DensityProvider>
  );
}
