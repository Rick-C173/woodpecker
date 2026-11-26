/**
 * 用户组域 queries/mutations（FR-18）：组管理（管理员）+ 库组授权（管理员/owner）。
 * 写操作成功后按前缀 invalidate（组列表/组员/库授权/库详情联动刷新）。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { components } from '@/api/schema';

type GroupItem = components['schemas']['service.GroupItem'];
type GroupMemberItem = components['schemas']['service.GroupMemberItem'];
type KBGroupRoleItem = components['schemas']['service.KBGroupRoleItem'];

/** 组列表（管理员）。 */
export function useGroupsList() {
  return useQuery({
    queryKey: ['admin', 'groups'],
    queryFn: async (): Promise<GroupItem[]> => {
      const { data } = await api.GET('/admin/groups', {});
      return data?.items ?? [];
    },
    staleTime: 30_000,
  });
}

/** 组成员列表（管理员）。 */
export function useGroupMembers(groupId: number, enabled = true) {
  return useQuery({
    queryKey: ['admin', 'groups', groupId, 'members'],
    queryFn: async (): Promise<GroupMemberItem[]> => {
      const { data } = await api.GET('/admin/groups/{id}/members', {
        params: { path: { id: groupId } },
      });
      return data?.items ?? [];
    },
    enabled: enabled && groupId > 0,
  });
}

/** 建组。GROUP_001 重名不阻止：由调用方 toast 提示并保留表单。 */
export function useCreateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: { name: string; description?: string }): Promise<GroupItem> => {
      const { data, response } = await api.POST('/admin/groups', { body });
      if (!response.ok || !data) throw new Error('创建用户组失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['admin', 'groups'] });
    },
  });
}

/** 改组名/描述。 */
export function useUpdateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: {
      id: number;
      body: { name?: string; description?: string };
    }): Promise<GroupItem> => {
      const { data, response } = await api.PATCH('/admin/groups/{id}', {
        params: { path: { id: input.id } },
        body: input.body,
      });
      if (!response.ok || !data) throw new Error('修改用户组失败');
      return data;
    },
    onSuccess: (_d, input) => {
      void qc.invalidateQueries({ queryKey: ['admin', 'groups'] });
      void qc.invalidateQueries({ queryKey: ['admin', 'groups', input.id, 'members'] });
    },
  });
}

/** 删组（级联解除组员与授权）。 */
export function useDeleteGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number): Promise<void> => {
      const { response } = await api.DELETE('/admin/groups/{id}', { params: { path: { id } } });
      if (!response.ok) throw new Error('删除用户组失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['admin', 'groups'] });
      void qc.invalidateQueries({ queryKey: ['admin', 'groups'] });
    },
  });
}

/** 加组员。GROUP_002 已在组内不阻止：由调用方 toast 提示。 */
export function useAddGroupMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { groupId: number; userId: number }): Promise<GroupMemberItem> => {
      const { data, response } = await api.POST('/admin/groups/{id}/members', {
        params: { path: { id: input.groupId } },
        body: { user_id: input.userId },
      });
      if (!response.ok || !data) throw new Error('添加成员失败');
      return data;
    },
    onSuccess: (_d, input) => {
      void qc.invalidateQueries({ queryKey: ['admin', 'groups', input.groupId, 'members'] });
      void qc.invalidateQueries({ queryKey: ['admin', 'groups'] });
    },
  });
}

/** 移出组（即时失去组授库访问）。 */
export function useRemoveGroupMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { groupId: number; userId: number }): Promise<void> => {
      const { response } = await api.DELETE('/admin/groups/{id}/members/{user}', {
        params: { path: { id: input.groupId, user: input.userId } },
      });
      if (!response.ok) throw new Error('移除成员失败');
    },
    onSuccess: (_d, input) => {
      void qc.invalidateQueries({ queryKey: ['admin', 'groups', input.groupId, 'members'] });
      void qc.invalidateQueries({ queryKey: ['admin', 'groups'] });
    },
  });
}

/** 库的组授权列表（管理员/owner）。 */
export function useKBGroupRoles(kbId: number, enabled = true) {
  return useQuery({
    queryKey: ['kbs', kbId, 'group-roles'],
    queryFn: async (): Promise<KBGroupRoleItem[]> => {
      const { data } = await api.GET('/kbs/{kb}/group-roles', {
        params: { path: { kb: kbId } },
      });
      return data?.items ?? [];
    },
    staleTime: 30_000,
    enabled: enabled && kbId > 0,
  });
}

/** 组授权到库。GROUP_003 已授权不阻止：由调用方 toast 提示。 */
export function useGrantGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: {
      kbId: number;
      groupId: number;
      role: string;
    }): Promise<KBGroupRoleItem> => {
      const { data, response } = await api.POST('/kbs/{kb}/group-roles', {
        params: { path: { kb: input.kbId } },
        body: { group_id: input.groupId, role: input.role },
      });
      if (!response.ok || !data) throw new Error('组授权失败');
      return data;
    },
    onSuccess: (_d, input) => {
      void qc.invalidateQueries({ queryKey: ['kbs', input.kbId, 'group-roles'] });
      void qc.invalidateQueries({ queryKey: ['admin', 'groups'] });
    },
  });
}

/** 变更组角色。 */
export function useUpdateGroupRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { kbId: number; groupId: number; role: string }): Promise<void> => {
      const { response } = await api.PATCH('/kbs/{kb}/group-roles/{group}', {
        params: { path: { kb: input.kbId, group: input.groupId } },
        body: { role: input.role },
      });
      if (!response.ok) throw new Error('变更组角色失败');
    },
    onSuccess: (_d, input) => {
      void qc.invalidateQueries({ queryKey: ['kbs', input.kbId, 'group-roles'] });
    },
  });
}

/** 撤销组授权（组员即时失访）。 */
export function useRevokeGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { kbId: number; groupId: number }): Promise<void> => {
      const { response } = await api.DELETE('/kbs/{kb}/group-roles/{group}', {
        params: { path: { kb: input.kbId, group: input.groupId } },
      });
      if (!response.ok) throw new Error('撤销组授权失败');
    },
    onSuccess: (_d, input) => {
      void qc.invalidateQueries({ queryKey: ['kbs', input.kbId, 'group-roles'] });
      void qc.invalidateQueries({ queryKey: ['admin', 'groups'] });
    },
  });
}
