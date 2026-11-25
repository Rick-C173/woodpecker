/** role.ts 单测：R6 权限判定与角色文案。 */

import { describe, expect, it } from 'vitest';
import { canIngest, canManageContent, normalizeRole, roleLabel } from './role';

describe('normalizeRole', () => {
  it('识别三种合法角色', () => {
    expect(normalizeRole('owner')).toBe('owner');
    expect(normalizeRole('editor')).toBe('editor');
    expect(normalizeRole('member')).toBe('member');
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

  it('member 只读不可接入', () => {
    expect(canIngest('member')).toBe(false);
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

describe('roleLabel', () => {
  it('输出中文文案', () => {
    expect(roleLabel('owner')).toBe('管理者');
    expect(roleLabel('editor')).toBe('编辑者');
    expect(roleLabel('member')).toBe('成员');
  });

  it('未知角色兜底', () => {
    expect(roleLabel('stranger')).toBe('stranger');
    expect(roleLabel(null)).toBe('成员');
  });
});
