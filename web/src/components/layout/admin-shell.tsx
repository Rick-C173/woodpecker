/**
 * 管理端（v2，规划 §4 一套框架两种模式）：
 * - ADMIN_NAV_GROUPS：管理侧导航数据（成员/集成/治理三组），由 AppShell 按 /admin/*
 *   路由切换侧栏变体时渲染——双栏不嵌套，管理端是同一壳的另一种模式而非第二套壳。
 * - AdminShell：仅负责内容区强制紧凑密度（局部 data-density 覆盖 html 档位，不落盘）
 *   + 内容留白 + Outlet；AdminGuard 仍在路由层包裹（非 is_admin → forbidden 页）。
 */

import { Outlet } from 'react-router-dom';
import type { LucideIcon } from 'lucide-react';
import {
  ClipboardList,
  Cog,
  KeyRound,
  Mail,
  MailPlus,
  MessagesSquare,
  Users,
  UsersRound,
  Wrench,
} from 'lucide-react';

export interface AdminNavItem {
  to: string;
  label: string;
  icon: LucideIcon;
}

export interface AdminNavGroup {
  label: string;
  items: AdminNavItem[];
}

/** 管理侧导航（批准 demo 结构：成员 / 集成 / 治理）。 */
export const ADMIN_NAV_GROUPS: AdminNavGroup[] = [
  {
    label: '成员',
    items: [
      { to: '/admin/users', label: '用户管理', icon: Users },
      { to: '/admin/invitations', label: '邀请管理', icon: MailPlus },
      { to: '/admin/groups', label: '用户组', icon: UsersRound },
    ],
  },
  {
    label: '集成',
    items: [
      { to: '/admin/email', label: '邮件配置', icon: Mail },
      { to: '/admin/oidc', label: 'SSO / OIDC', icon: KeyRound },
      { to: '/admin/models', label: '模型服务', icon: Wrench },
    ],
  },
  {
    label: '治理',
    items: [
      { to: '/admin/audit', label: '审计日志', icon: ClipboardList },
      { to: '/admin/settings', label: '系统设置', icon: Cog },
      { to: '/admin/feedbacks', label: '反馈列表', icon: MessagesSquare },
    ],
  },
];

/** 管理端路由 → 页面名（AppShell 顶栏面包屑用）。 */
export const ADMIN_LABELS: Record<string, string> = Object.fromEntries(
  ADMIN_NAV_GROUPS.flatMap((g) => g.items.map((i) => [i.to, i.label])),
);

/** 管理端壳：compact 密度子树（不落盘）+ 内容留白 + Outlet。 */
export function AdminShell() {
  return (
    <div data-density="compact" className="h-full px-7 py-6">
      <Outlet />
    </div>
  );
}
