/**
 * 个人 API Token 域 queries/mutations（M15 / FR-20 契约）：
 * 列表 staleTime 30s；创建/吊销成功后 invalidate 列表；明文仅创建响应返回一次。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/api/client';
import { qk } from '@/lib/queryKeys';
import type { components } from '@/api/schema';

type APITokenItem = components['schemas']['service.APITokenItem'];
type CreatedToken = components['schemas']['service.CreatedToken'];

/** 我的 token 列表（含已吊销；prefix 展示时拼 wpk_ 前缀）。 */
export function useTokensList(enabled = true) {
  return useQuery({
    queryKey: qk.tokens.list,
    queryFn: async (): Promise<APITokenItem[]> => {
      const { data } = await api.GET('/tokens', {});
      return data?.items ?? [];
    },
    staleTime: 30_000,
    enabled,
  });
}

/** 创建 token（TOK_001 超 20 个由调用方 toast 提示并保留表单）。明文仅本次响应返回。 */
export function useCreateToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: {
      name: string;
      /** 可空=永久；1~3650 */
      expires_in_days?: number;
    }): Promise<CreatedToken> => {
      const { data, response } = await api.POST('/tokens', { body });
      if (!response.ok || !data) throw new Error('创建 Token 失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.tokens.list });
    },
  });
}

/** 吊销自己的 token（软吊销；他人与不存在统一 404）。 */
export function useRevokeToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number): Promise<void> => {
      const { response } = await api.DELETE('/tokens/{id}', {
        params: { path: { id } },
      });
      if (!response.ok) throw new Error('吊销失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.tokens.list });
    },
  });
}
