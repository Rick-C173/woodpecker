/**
 * 管理端布局（F6）：二级导航（用户/模型/审计/设置/反馈）+ 返回知识库 + Outlet。
 * AdminGuard 在路由层包裹（非 is_admin → forbidden 页）。
 */

import { NavLink, Outlet } from 'react-router-dom';
import {
  ArrowLeft,
  BookOpenText,
  ClipboardList,
  Cog,
  KeyRound,
  MailPlus,
  MessagesSquare,
  Users,
  UsersRound,
  Wrench,
} from 'lucide-react';

const ADMIN_NAV = [
  { to: '/admin/users', label: '用户管理', icon: Users },
  { to: '/admin/groups', label: '用户组', icon: UsersRound },
  { to: '/admin/invitations', label: '邀请管理', icon: MailPlus },
  { to: '/admin/email', label: '邮件配置', icon: MailPlus },
  { to: '/admin/oidc', label: 'SSO 登录', icon: KeyRound },
  { to: '/admin/models', label: '模型服务', icon: Wrench },
  { to: '/admin/audit', label: '审计日志', icon: ClipboardList },
  { to: '/admin/settings', label: '系统设置', icon: Cog },
  { to: '/admin/feedbacks', label: '反馈列表', icon: MessagesSquare },
];

export function AdminShell() {
  return (
    <div className="flex h-full">
      <nav className="flex w-60 flex-none flex-col gap-1 border-r border-[var(--border)] px-2.5 py-4">
        <NavLink to="/kbs" className="nav-item mb-2 text-[var(--faint)]">
          <ArrowLeft aria-hidden="true" />
          返回知识库
        </NavLink>
        <p className="flex items-center gap-1.5 px-3 pb-1 text-[11px] font-semibold tracking-wide text-[var(--faint)]">
          <BookOpenText className="size-3.5" aria-hidden="true" />
          管理后台
        </p>
        {ADMIN_NAV.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}
          >
            <item.icon aria-hidden="true" />
            {item.label}
          </NavLink>
        ))}
      </nav>
      <div className="min-w-0 flex-1 overflow-auto px-7 py-6">
        <Outlet />
      </div>
    </div>
  );
}
