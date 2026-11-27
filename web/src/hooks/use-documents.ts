/**
 * 文档域 queries/mutations（架构 §1.2 / §4.4）：
 * 列表与详情 staleTime 5s + 函数式 refetchInterval（pending/processing 活跃 → 3s，全终态停）；
 * 一切写操作成功后按文档前缀 invalidate，让新文档立即进入轮询。
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/api/client';
import { postUrlIngest, postUploadFiles, type UploadResult } from '@/api/manual-body';
import { qk } from '@/lib/queryKeys';
import { documentsRefetchInterval } from '@/lib/poll';
import type { components } from '@/api/schema';

type DocumentItem = components['schemas']['service.DocumentItem'];
type DocumentDetail = components['schemas']['service.DocumentDetail'];

/** 文档列表过滤条件（type/status 数字枚举见 API设计 §3.6）。 */
export interface DocumentFilters {
  type?: number;
  status?: number;
  page?: number;
  page_size?: number;
}

export interface DocumentPage {
  items: DocumentItem[];
  total: number;
  page: number;
  page_size: number;
}

const EMPTY_PAGE: DocumentPage = { items: [], total: 0, page: 1, page_size: 20 };

/** 文档列表（分页，新到旧；含 can_* 操作字段）。 */
export function useDocumentsList(kbId: number, filters: DocumentFilters = {}) {
  return useQuery({
    queryKey: qk.kbs.documents(kbId, { ...filters } as Record<string, unknown>),
    queryFn: async (): Promise<DocumentPage> => {
      const { data } = await api.GET('/kbs/{kb}/documents', {
        params: { path: { kb: kbId }, query: { ...filters } },
      });
      if (!data) return EMPTY_PAGE;
      return {
        items: data.items ?? [],
        total: data.total ?? 0,
        page: data.page ?? 1,
        page_size: data.page_size ?? 20,
      };
    },
    staleTime: 5_000,
    refetchInterval: documentsRefetchInterval,
    refetchIntervalInBackground: false,
    enabled: Number.isFinite(kbId) && kbId > 0,
  });
}

/** 文档详情（状态、错误、文件清单、任务历史）。 */
export function useDocumentDetail(kbId: number, docId: number) {
  return useQuery({
    queryKey: qk.kbs.document(kbId, docId),
    queryFn: async (): Promise<DocumentDetail> => {
      const { data, response } = await api.GET('/kbs/{kb}/documents/{doc}', {
        params: { path: { kb: kbId, doc: docId } },
      });
      if (!response.ok || !data) throw new Error('文档加载失败');
      return data;
    },
    staleTime: 5_000,
    refetchInterval: documentsRefetchInterval,
    refetchIntervalInBackground: false,
    enabled: Number.isFinite(kbId) && kbId > 0 && Number.isFinite(docId) && docId > 0,
  });
}

/** 文档前缀统一失效（覆盖列表任意 filters 与详情）。 */
function useInvalidateDocuments(kbId: number) {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ['kbs', kbId, 'documents'] });
  };
}

/** Git 接入（编辑者+）：DOC_001/003 错误由调用方 toast 展示。 */
export function useGitIngest(kbId: number) {
  const invalidate = useInvalidateDocuments(kbId);
  return useMutation({
    mutationFn: async (body: {
      repo_url: string;
      git_ref?: string;
      credential?: { kind: 'token' | 'ssh'; secret: string };
      ignore_rules?: string[];
    }): Promise<DocumentItem> => {
      const { data, response } = await api.POST('/kbs/{kb}/documents/git', {
        params: { path: { kb: kbId } },
        body,
      });
      if (!response.ok || !data) throw new Error('Git 接入失败');
      return data;
    },
    onSuccess: invalidate,
  });
}

/** 文件上传（编辑者+）：multipart files[]，rejected 逐个报 DOC_002。 */
export function useUploadFiles(kbId: number) {
  const invalidate = useInvalidateDocuments(kbId);
  return useMutation({
    mutationFn: async (files: File[]): Promise<UploadResult> => {
      const { data, response } = await postUploadFiles(kbId, files);
      if (!response.ok || !data) throw new Error('文件上传失败');
      return data;
    },
    onSuccess: invalidate,
  });
}

/** 网页接入（编辑者+）。 */
export function useUrlIngest(kbId: number) {
  const invalidate = useInvalidateDocuments(kbId);
  return useMutation({
    mutationFn: async (url: string): Promise<DocumentItem> => {
      const { data, response } = await postUrlIngest(kbId, { url });
      if (!response.ok || !data) throw new Error('网页接入失败');
      return data;
    },
    onSuccess: invalidate,
  });
}

/** 重试摄取（编辑者+，仅 failed；409 DOC_TASK_RUNNING 由调用方 toast）。 */
export function useRetryDocument(kbId: number) {
  const invalidate = useInvalidateDocuments(kbId);
  return useMutation({
    mutationFn: async (docId: number): Promise<void> => {
      const { response } = await api.POST('/kbs/{kb}/documents/{doc}/retry', {
        params: { path: { kb: kbId, doc: docId } },
      });
      if (!response.ok) throw new Error('重试失败');
    },
    onSuccess: invalidate,
  });
}

/** 手动同步（编辑者+，仅 git 类型）。 */
export function useSyncDocument(kbId: number) {
  const invalidate = useInvalidateDocuments(kbId);
  return useMutation({
    mutationFn: async (docId: number): Promise<void> => {
      const { response } = await api.POST('/kbs/{kb}/documents/{doc}/sync', {
        params: { path: { kb: kbId, doc: docId } },
      });
      if (!response.ok) throw new Error('同步失败');
    },
    onSuccess: invalidate,
  });
}

/** 开关定时同步（M14，git 类型；成功后 invalidate 拿最新 auto_sync）。 */
export function useSetAutoSync(kbId: number) {
  const invalidate = useInvalidateDocuments(kbId);
  return useMutation({
    mutationFn: async ({ docId, autoSync }: { docId: number; autoSync: boolean }): Promise<void> => {
      const { response } = await api.PATCH('/kbs/{kb}/documents/{doc}/auto-sync', {
        params: { path: { kb: kbId, doc: docId } },
        body: { auto_sync: autoSync },
      });
      if (!response.ok) throw new Error('设置失败');
    },
    onSuccess: invalidate,
  });
}

/** 生成/轮换 webhook token（M14）：明文仅本次响应返回。 */
export function useRotateWebhookSecret(kbId: number) {
  const invalidate = useInvalidateDocuments(kbId);
  return useMutation({
    mutationFn: async (docId: number): Promise<{ webhook_token: string; webhook_url: string }> => {
      const { data, response } = await api.POST('/kbs/{kb}/documents/{doc}/webhook-secret', {
        params: { path: { kb: kbId, doc: docId } },
      });
      if (!response.ok || !data) throw new Error('生成失败');
      return { webhook_token: data.webhook_token ?? '', webhook_url: data.webhook_url ?? '' };
    },
    onSuccess: invalidate,
  });
}

/** 重建索引（管理者+，仅 ready）。 */
export function useReindexDocument(kbId: number) {
  const invalidate = useInvalidateDocuments(kbId);
  return useMutation({
    mutationFn: async (docId: number): Promise<void> => {
      const { response } = await api.POST('/kbs/{kb}/documents/{doc}/reindex', {
        params: { path: { kb: kbId, doc: docId } },
      });
      if (!response.ok) throw new Error('重建索引失败');
    },
    onSuccess: invalidate,
  });
}

/** 删除文档（管理者+，级联清分块/清单/任务）。 */
export function useDeleteDocument(kbId: number) {
  const invalidate = useInvalidateDocuments(kbId);
  return useMutation({
    mutationFn: async (docId: number): Promise<void> => {
      const { response } = await api.DELETE('/kbs/{kb}/documents/{doc}', {
        params: { path: { kb: kbId, doc: docId } },
      });
      if (!response.ok) throw new Error('删除文档失败');
    },
    onSuccess: invalidate,
  });
}
