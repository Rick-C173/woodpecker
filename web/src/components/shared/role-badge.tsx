/**
 * RoleBadge：库内角色徽标（.badge role-* 视觉类名，暗色 admin 徽标辉光走 CSS）。
 */

import { normalizeRole, roleLabel } from '@/lib/role';
import { cn } from '@/lib/utils';

export function RoleBadge({ role, className }: { role?: string | null; className?: string }) {
  const r = normalizeRole(role);
  if (!r) return null;
  return <span className={cn('badge', `role-${r}`, className)}>{roleLabel(r)}</span>;
}
