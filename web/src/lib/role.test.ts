/** role.ts 单测：R6 权限判定与角色文案（含 FR-17 org 只读访客）。 */

import { describe, expect, it } from 'vitest';
import { canIngest, canManageContent, canManageMembers, normalizeRole, roleLabel } from './role';

describe('normalizeRole', () => {
  it('识别四种合法角色（含 FR-17 org 访客）', () => {
    expect(normalizeRole('owner')).toBe('owner');
    expect(normalizeRole('editor')).toBe('editor');
    expect(normalizeRole('member')).toBe('member');
    expect(normalizeRole('org')).toBe('org');
  });

  it('非法/缺失角色返回 null', () => {
    expect(normalizeRole('admin')).toBeNull();
    expect(normalizeRole('')).toBeNull();
    expect(normalizeRole(null)).toBeNull();
    expect(normalizeRole(undefined)).toBeNull();
  });
});

describe('canIngest（编辑者+）', () => {
  it('owner/editor 可接入', () => {
    expect(canIngest('owner')).toBe(true);
    expect(canIngest('editor')).toBe(true);
  });

  it('member/org 只读不可接入', () => {
    expect(canIngest('member')).toBe(false);
    expect(canIngest('org')).toBe(false);
  });

  it('管理员恒可接入', () => {
    expect(canIngest('member', true)).toBe(true);
    expect(canIngest(null, true)).toBe(true);
  });
});

describe('canManageContent（管理者+）', () => {
  it('owner 可删改内容', () => {
    expect(canManageContent('owner')).toBe(true);
    expect(canManageContent('editor')).toBe(false);
    expect(canManageContent('member')).toBe(false);
  });

  it('管理员恒可管理', () => {
    expect(canManageContent('member', true)).toBe(true);
  });
});

describe('canManageMembers（R6 扩展：管理员或库管理者）', () => {
  it('owner 可授权成员', () => {
    expect(canManageMembers('owner', false)).toBe(true);
  });

  it('editor/member/org 不可授权', () => {
    expect(canManageMembers('editor', false)).toBe(false);
    expect(canManageMembers('member', false)).toBe(false);
    expect(canManageMembers('org', false)).toBe(false);
    expect(canManageMembers(null, false)).toBe(false);
  });

  it('管理员恒可授权', () => {
    expect(canManageMembers('member', true)).toBe(true);
  });
});

describe('roleLabel', () => {
  it('输出中文文案', () => {
    expect(roleLabel('owner')).toBe('管理者');
    expect(roleLabel('editor')).toBe('编辑者');
    expect(roleLabel('member')).toBe('成员');
    expect(roleLabel('org')).toBe('全员只读');
  });

  it('未知角色兜底', () => {
    expect(roleLabel('stranger')).toBe('stranger');
    expect(roleLabel(null)).toBe('成员');
  });
});
