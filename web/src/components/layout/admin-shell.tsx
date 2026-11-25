/**
 * 管理端布局骨架（F6 填充）。AdminGuard 在路由层包裹，这里只负责二级导航与 Outlet。
 */

import { NavLink, Outlet } from 'react-router-dom';

const ADMIN_NAV = [
  { to: '/admin/users', label: '用户管理' },
  { to: '/admin/models', label: '模型服务' },
  { to: '/admin/audit', label: '审计日志' },
  { to: '/admin/settings', label: '系统设置' },
  { to: '/admin/feedbacks', label: '反馈列表' },
];

export function AdminShell() {
  return (
    <div className="flex h-full">
      <nav className="flex w-48 flex-none flex-col gap-1 border-r border-[var(--border)] px-2.5 py-4">
        {ADMIN_NAV.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}
          >
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
