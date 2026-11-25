/**
 * 路由表 + 守卫（架构 §3）。react-router v7 data router，不用 loader/action——
 * 数据获取全部归 TanStack Query；守卫用「路由包装组件 + <Navigate replace>」。
 *
 * 守卫流程：有 token? → 无 → /login?from=当前路径；must_change_password → /change-password
 * （除该页外全部拦截）；路径以 /admin 开头 → 非 is_admin → forbidden 403 页。
 * 双保险：client 中间件对 AUTH_007 全局广播跳改密（AuthEventBridge 统一导航）。
 */

import { Navigate, createBrowserRouter, useLocation, useNavigate } from 'react-router-dom';
import type { ReactElement } from 'react';
import { useEffect } from 'react';
import { AppShell } from '@/components/layout/app-shell';
import { AdminShell } from '@/components/layout/admin-shell';
import { useAuth } from '@/hooks/use-auth';
import { onAuthEvent } from '@/lib/auth';

import { LoginPage } from '@/pages/login';
import { ChangePasswordPage } from '@/pages/change-password';
import { KbsPage } from '@/pages/kbs';
import { KbDetailPage } from '@/pages/kb-detail';
import { KbDocumentDetailPage } from '@/pages/kb-document-detail';
import { KbSearchPage } from '@/pages/kb-search';
import { KbChatPage } from '@/pages/kb-chat';
import { AdminUsersPage } from '@/pages/admin/users';
import { AdminModelServicesPage } from '@/pages/admin/model-services';
import { AdminAuditLogsPage } from '@/pages/admin/audit-logs';
import { AdminSettingsPage } from '@/pages/admin/settings';
import { AdminFeedbacksPage } from '@/pages/admin/feedbacks';
import { NotFoundPage } from '@/pages/not-found';
import { ForbiddenPage } from '@/pages/forbidden';

/** 未登录 → /login?from=当前路径（AuthGuard）。 */
function AuthGuard({ children }: { children: ReactElement }) {
  const { loggedIn, user } = useAuth();
  const location = useLocation();
  if (!loggedIn) {
    return <Navigate replace to={`/login?from=${encodeURIComponent(location.pathname)}`} />;
  }
  // must_change_password：除改密页外全部拦截
  if (user?.must_change_password && location.pathname !== '/change-password') {
    return <Navigate replace to="/change-password" />;
  }
  return children;
}

/** 已登录访问 /login → /kbs（LoginGuard）。 */
function LoginGuard({ children }: { children: ReactElement }) {
  const { loggedIn } = useAuth();
  if (loggedIn) {
    return <Navigate replace to="/kbs" />;
  }
  return children;
}

/** 管理端守卫（内层）：非 is_admin → forbidden 页（不 redirect，明确告知无权）。 */
function AdminGuard({ children }: { children: ReactElement }) {
  const { user } = useAuth();
  if (!user?.is_admin) {
    return <ForbiddenPage />;
  }
  return children;
}

/** 全局认证事件桥：client 中间件广播 401 / AUTH_007 → 统一导航。 */
function AuthEventBridge() {
  const navigate = useNavigate();
  useEffect(() => {
    return onAuthEvent((event) => {
      if (event === 'logout') {
        navigate(`/login?from=${encodeURIComponent(window.location.pathname)}`, { replace: true });
      } else {
        navigate('/change-password', { replace: true });
      }
    });
  }, [navigate]);
  return null;
}

export const router = createBrowserRouter([
  { path: '/login', element: <LoginGuard><LoginPage /></LoginGuard> },
  { path: '/change-password', element: <ChangePasswordPage /> },
  {
    path: '/',
    element: (
      <>
        <AuthEventBridge />
        <AppShell />
      </>
    ),
    children: [
      { index: true, element: <Navigate replace to="/kbs" /> },
      { path: 'kbs', element: <AuthGuard><KbsPage /></AuthGuard> },
      { path: 'kbs/:kbId', element: <AuthGuard><KbDetailPage /></AuthGuard> },
      {
        path: 'kbs/:kbId/documents/:docId',
        element: <AuthGuard><KbDocumentDetailPage /></AuthGuard>,
      },
      { path: 'kbs/:kbId/search', element: <AuthGuard><KbSearchPage /></AuthGuard> },
      { path: 'kbs/:kbId/chat', element: <AuthGuard><KbChatPage /></AuthGuard> },
      { path: 'kbs/:kbId/chat/:cId', element: <AuthGuard><KbChatPage /></AuthGuard> },
      {
        path: 'admin',
        element: <AdminShell />,
        children: [
          { index: true, element: <Navigate replace to="/admin/users" /> },
          {
            path: 'users',
            element: (
              <AuthGuard>
                <AdminGuard><AdminUsersPage /></AdminGuard>
              </AuthGuard>
            ),
          },
          {
            path: 'models',
            element: (
              <AuthGuard>
                <AdminGuard><AdminModelServicesPage /></AdminGuard>
              </AuthGuard>
            ),
          },
          {
            path: 'audit',
            element: (
              <AuthGuard>
                <AdminGuard><AdminAuditLogsPage /></AdminGuard>
              </AuthGuard>
            ),
          },
          {
            path: 'settings',
            element: (
              <AuthGuard>
                <AdminGuard><AdminSettingsPage /></AdminGuard>
              </AuthGuard>
            ),
          },
          {
            path: 'feedbacks',
            element: (
              <AuthGuard>
                <AdminGuard><AdminFeedbacksPage /></AdminGuard>
              </AuthGuard>
            ),
          },
        ],
      },
      { path: '*', element: <AuthGuard><NotFoundPage /></AuthGuard> },
    ],
  },
]);
