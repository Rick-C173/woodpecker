/**
 * 知识库域 queries/mutations（架构 §1.2 失效纪律）：
 * 列表/详情 staleTime 30s；写操作成功后按资源前缀 invalidate，不做乐观更新。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/api/client';
import { qk } from '@/lib/queryKeys';
import type { components } from '@/api/schema';

type KBItem = components['schemas']['service.KBItem'];

/** 我可见的知识库列表（管理员=全部，含 my_role）。 */
export function useKbsList(enabled = true) {
  return useQuery({
    queryKey: qk.kbs.list,
    queryFn: async (): Promise<KBItem[]> => {
      const { data } = await api.GET('/kbs', {});
      return data?.items ?? [];
    },
    staleTime: 30_000,
    enabled,
  });
}

/** 知识库详情（非成员 404 → 调用方按 EmptyState 渲染）。 */
export function useKbDetail(kbId: number, enabled = true) {
  return useQuery({
    queryKey: qk.kbs.detail(kbId),
    queryFn: async (): Promise<KBItem> => {
      const { data, response } = await api.GET('/kbs/{kb}', {
        params: { path: { kb: kbId } },
      });
      if (!response.ok || !data) {
        throw new Error('知识库加载失败');
      }
      return data;
    },
    staleTime: 30_000,
    enabled: enabled && Number.isFinite(kbId) && kbId > 0,
  });
}

/** 建库（管理员）。KB_001 名称重复不阻止：由调用方以 toast 提示并保留表单。 */
export function useCreateKb() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: { name: string; description?: string }): Promise<KBItem> => {
      const { data, response } = await api.POST('/kbs', { body });
      if (!response.ok || !data) throw new Error('创建知识库失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.kbs.list });
    },
  });
}

/** 改名/改描述（管理员）。 */
export function useUpdateKb(kbId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: { name?: string; description?: string }): Promise<KBItem> => {
      const { data, response } = await api.PATCH('/kbs/{kb}', {
        params: { path: { kb: kbId } },
        body,
      });
      if (!response.ok || !data) throw new Error('修改知识库失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.kbs.list });
      void qc.invalidateQueries({ queryKey: qk.kbs.detail(kbId) });
    },
  });
}

/** 删库（管理员，级联；二次确认由前端 ConfirmDialog 承担）。 */
export function useDeleteKb() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (kbId: number): Promise<void> => {
      const { response } = await api.DELETE('/kbs/{kb}', { params: { path: { kb: kbId } } });
      if (!response.ok) throw new Error('删除知识库失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.kbs.list });
    },
  });
}
