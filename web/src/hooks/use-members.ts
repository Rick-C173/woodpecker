/**
 * 成员管理 queries/mutations（管理员专用端点，API设计 §3.5）。
 * 成员列表 staleTime 30s；增/改/移除成功后 invalidate 成员前缀。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/api/client';
import { qk } from '@/lib/queryKeys';
import type { components } from '@/api/schema';

type KBMemberItem = components['schemas']['service.KBMemberItem'];

export type MemberRole = 'owner' | 'editor' | 'member';

/** 成员列表（user_id/username/role/加入时间）。 */
export function useMembers(kbId: number, enabled = true) {
  return useQuery({
    queryKey: qk.kbs.members(kbId),
    queryFn: async (): Promise<KBMemberItem[]> => {
      const { data } = await api.GET('/kbs/{kb}/members', {
        params: { path: { kb: kbId } },
      });
      return data?.items ?? [];
    },
    staleTime: 30_000,
    enabled: enabled && Number.isFinite(kbId) && kbId > 0,
  });
}

/** 添加成员并指派角色（409 已是成员由调用方 toast）。 */
export function useAddMember(kbId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: { user_id: number; role: MemberRole }): Promise<KBMemberItem> => {
      const { data, response } = await api.POST('/kbs/{kb}/members', {
        params: { path: { kb: kbId } },
        body,
      });
      if (!response.ok || !data) throw new Error('添加成员失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.kbs.members(kbId) });
    },
  });
}

/** 变更成员角色。 */
export function useUpdateMemberRole(kbId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ userId, role }: { userId: number; role: MemberRole }): Promise<void> => {
      const { response } = await api.PATCH('/kbs/{kb}/members/{user}', {
        params: { path: { kb: kbId, user: userId } },
        body: { role },
      });
      if (!response.ok) throw new Error('角色变更失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.kbs.members(kbId) });
    },
  });
}

/** 移除成员（其历史会话保留但立即失访）。 */
export function useRemoveMember(kbId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (userId: number): Promise<void> => {
      const { response } = await api.DELETE('/kbs/{kb}/members/{user}', {
        params: { path: { kb: kbId, user: userId } },
      });
      if (!response.ok) throw new Error('移除成员失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.kbs.members(kbId) });
    },
  });
}
