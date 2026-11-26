/**
 * use-role：从登录态（is_admin）+ 库详情（my_role）派生 R6 权限集。
 * UI 按钮显隐的唯一依据（PRD R6 权限矩阵）。
 */

import { useAuth } from '@/hooks/use-auth';
import { canIngest, canManageContent, canManageMembers } from '@/lib/role';

export interface RolePermissions {
  /** 系统管理员 */
  isAdmin: boolean;
  /** 接入权限（编辑者+）：Git/上传/URL/重试/同步按钮 */
  mayIngest: boolean;
  /** 内容管理权限（管理者+）：删除文档/重建索引按钮 */
  mayManageContent: boolean;
  /** 成员管理 Tab 可见性（R6 扩展：管理员或库内管理者） */
  mayManageMembers: boolean;
}

/** 传入库内 my_role；管理员在任意库内拥有全部能力。 */
export function useRole(myRole: string | null | undefined): RolePermissions {
  const { user } = useAuth();
  const isAdmin = user?.is_admin ?? false;
  return {
    isAdmin,
    mayIngest: canIngest(myRole, isAdmin),
    mayManageContent: canManageContent(myRole, isAdmin),
    mayManageMembers: canManageMembers(myRole, isAdmin),
  };
}
