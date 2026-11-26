/**
 * R6 角色权限判定（纯函数，供 use-role hook 与单测使用）。
 * PRD R6：owner=管理者（可删改内容、授权成员）editor=编辑者（可补充内容）member=成员（只读）；
 * FR-17：org=全员可见库的只读访客（非成员，等效 member 读权限）；
 * 管理员（系统级）在知识库内默认拥有全部能力，由调用方以 isAdmin 参数并入。
 */

/** 库内角色（与后端 my_role / members.role 一致）。 */
export type KbRole = 'owner' | 'editor' | 'member' | 'org';

export const KB_ROLES: readonly KbRole[] = ['owner', 'editor', 'member', 'org'];

/** 角色 → 中文徽标文案。 */
export const ROLE_LABELS: Record<KbRole, string> = {
  owner: '管理者',
  editor: '编辑者',
  member: '成员',
  org: '全员只读',
};

/** 归一化后端角色串；非法/缺失返回 null（调用方按 member 兜底渲染）。 */
export function normalizeRole(role: string | null | undefined): KbRole | null {
  if (role === 'owner' || role === 'editor' || role === 'member' || role === 'org') return role;
  return null;
}

/** 角色 → 中文文案；未知角色兜底展示原文或「成员」。 */
export function roleLabel(role: string | null | undefined): string {
  const r = normalizeRole(role);
  return r ? ROLE_LABELS[r] : (role ?? '成员');
}

/**
 * 接入权限（编辑者+）：Git 接入 / 上传 / 网页接入 / 重试 / 同步。
 * 管理员恒为 true；org 访客只读。
 */
export function canIngest(role: string | null | undefined, isAdmin = false): boolean {
  if (isAdmin) return true;
  const r = normalizeRole(role);
  return r === 'owner' || r === 'editor';
}

/**
 * 内容管理权限（管理者+）：删除文档 / 重建索引。
 * 管理员恒为 true。
 */
export function canManageContent(role: string | null | undefined, isAdmin = false): boolean {
  if (isAdmin) return true;
  return normalizeRole(role) === 'owner';
}

/** 成员管理入口可见性（R6 扩展：管理员或库内管理者；owner 不能变更/移除自己由后端拦截）。 */
export function canManageMembers(role: string | null | undefined, isAdmin: boolean): boolean {
  if (isAdmin) return true;
  return normalizeRole(role) === 'owner';
}
