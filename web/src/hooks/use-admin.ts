/**
 * 管理端 queries/mutations（架构 §1.2 失效纪律；API设计 §3.2/§3.3/§3.4）。
 * 管理端列表 staleTime 15s；写操作成功后按资源前缀 invalidate。
 * 请求体缺注解的端点走 api/manual-body.ts（createUser/patchUser/patchSettings）。
 */

import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { components } from '@/api/schema';
import { api } from '@/api/client';
import {
  patchAdminSettings,
  patchAdminUser,
  patchModelEntry,
  postCreateUser,
  postModelEntry,
  postResetPassword,
  type CreateUserReq,
  type PatchSettingsReq,
  type PatchUserReq,
} from '@/api/manual-body';
import { qk } from '@/lib/queryKeys';

type AdminUserItem = components['schemas']['service.AdminUserItem'];
type CreateUserResp = components['schemas']['handler.createUserResp'];
type ResetPasswordResp = components['schemas']['handler.resetPasswordResp'];
type ModelServiceItem = components['schemas']['service.ModelServiceItem'];
type ModelEntryItem = components['schemas']['service.ModelEntryItem'];
type SettingsView = components['schemas']['service.SettingsView'];
type AuditRow = components['schemas']['repo.AuditRow'];
type FeedbackAdminRow = components['schemas']['repo.FeedbackAdminRow'];

/* ---------------- 用户管理（FR-13） ---------------- */

export interface AdminUsersFilters {
  q: string;
  /** 1=启用 2=禁用；null=全部 */
  status: number | null;
  page: number;
}

export interface AdminUsersPage {
  items: AdminUserItem[];
  total: number;
  page: number;
  page_size: number;
}

export function useAdminUsers(filters: AdminUsersFilters, enabled = true) {
  return useQuery({
    queryKey: [...qk.admin.users, filters],
    queryFn: async (): Promise<AdminUsersPage> => {
      const { data, response } = await api.GET('/admin/users', {
        params: {
          query: {
            q: filters.q || undefined,
            status: filters.status ?? undefined,
            page: filters.page,
          },
        },
      });
      if (!response.ok) throw new Error('用户列表加载失败');
      return {
        items: data?.items ?? [],
        total: data?.total ?? 0,
        page: data?.page ?? 1,
        page_size: data?.page_size ?? 20,
      };
    },
    staleTime: 15_000,
    placeholderData: keepPreviousData,
    enabled,
  });
}

/** 创建用户：响应含一次性初始密码（仅此一次展示）。 */
export function useCreateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: CreateUserReq): Promise<CreateUserResp> => {
      const { data, response } = await postCreateUser(body);
      if (!response.ok || !data) throw new Error('创建用户失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.admin.users });
    },
  });
}

/** 禁用/启用、设/撤管理员（USER_002/003 由后端 422 返回，调用方 toast e.message）。 */
export function usePatchUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, body }: { id: number; body: PatchUserReq }): Promise<void> => {
      const { response } = await patchAdminUser(id, body);
      if (!response.ok) throw new Error('操作失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.admin.users });
    },
  });
}

/** 重置密码：响应含一次性初始密码。 */
export function useResetPassword() {
  return useMutation({
    mutationFn: async (id: number): Promise<ResetPasswordResp> => {
      const { data, response } = await postResetPassword(id);
      if (!response.ok || !data) throw new Error('重置密码失败');
      return data;
    },
  });
}

/* ---------------- 模型服务与条目（FR-12） ---------------- */

export function useModelServices(enabled = true) {
  return useQuery({
    queryKey: [...qk.admin.models, 'services'],
    queryFn: async (): Promise<ModelServiceItem[]> => {
      const { data, response } = await api.GET('/admin/model-services', {});
      if (!response.ok) throw new Error('模型服务列表加载失败');
      return data?.items ?? [];
    },
    staleTime: 15_000,
    enabled,
  });
}

export interface ModelEntriesFilters {
  service_id: number | null;
  purpose: string | null;
}

export function useModelEntries(filters: ModelEntriesFilters, enabled = true) {
  return useQuery({
    queryKey: [...qk.admin.models, 'entries', filters],
    queryFn: async (): Promise<ModelEntryItem[]> => {
      const { data, response } = await api.GET('/admin/model-entries', {
        params: {
          query: {
            service_id: filters.service_id ?? undefined,
            purpose: filters.purpose ?? undefined,
          },
        },
      });
      if (!response.ok) throw new Error('模型条目列表加载失败');
      return data?.items ?? [];
    },
    staleTime: 15_000,
    enabled,
  });
}

export interface CreateModelServiceReq {
  name: string;
  base_url: string;
  /** AES-GCM 加密落库、永不回显 */
  api_key?: string;
}

export function useCreateModelService() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: CreateModelServiceReq): Promise<ModelServiceItem> => {
      const { data, response } = await api.POST('/admin/model-services', { body });
      if (!response.ok || !data) throw new Error('创建模型服务失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.admin.models });
    },
  });
}

export interface PatchModelServiceReq {
  /** key 字段缺省=不修改 */
  api_key?: string;
  base_url?: string;
  enabled?: boolean;
  name?: string;
}

export function usePatchModelService() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      id,
      body,
    }: {
      id: number;
      body: PatchModelServiceReq;
    }): Promise<ModelServiceItem> => {
      const { data, response } = await api.PATCH('/admin/model-services/{id}', {
        params: { path: { id } },
        body,
      });
      if (!response.ok || !data) throw new Error('修改模型服务失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.admin.models });
    },
  });
}

export function useDeleteModelService() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number): Promise<void> => {
      const { response } = await api.DELETE('/admin/model-services/{id}', {
        params: { path: { id } },
      });
      if (!response.ok) throw new Error('删除模型服务失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.admin.models });
    },
  });
}

/** 测试连接：响应 {chat, embedding, rerank}，值 "ok" 或 "err: <原因>"。 */
export type ModelTestResult = { chat?: string; embedding?: string; rerank?: string };

export function useTestModelService() {
  return useMutation({
    mutationFn: async (id: number): Promise<ModelTestResult> => {
      const { data, response } = await api.POST('/admin/model-services/{id}/test', {
        params: { path: { id } },
      });
      if (!response.ok || !data) throw new Error('测试连接失败');
      return { chat: data.chat, embedding: data.embedding, rerank: data.rerank };
    },
  });
}

export interface CreateModelEntryReq {
  service_id: number;
  /** chat|embedding|rerank */
  purpose: string;
  model_name: string;
  /** 模型参数对象（temperature/top_p/dimensions/top_n 等），后端存 JSON 串 */
  params?: Record<string, unknown>;
  /** true 直接设为该用途默认（同用途唯一默认，原默认让位） */
  is_default?: boolean;
}

export function useCreateModelEntry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: CreateModelEntryReq): Promise<ModelEntryItem> => {
      const { data, response } = await postModelEntry(body);
      if (!response.ok || !data) throw new Error('创建模型条目失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.admin.models });
    },
  });
}

export interface PatchModelEntryReq {
  is_default?: boolean;
  model_name?: string;
  params?: Record<string, unknown>;
  purpose?: string;
}

export function usePatchModelEntry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      id,
      body,
    }: {
      id: number;
      body: PatchModelEntryReq;
    }): Promise<ModelEntryItem> => {
      const { data, response } = await patchModelEntry(id, body);
      if (!response.ok || !data) throw new Error('修改模型条目失败');
      return data;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.admin.models });
    },
  });
}

export function useDeleteModelEntry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number): Promise<void> => {
      const { response } = await api.DELETE('/admin/model-entries/{id}', {
        params: { path: { id } },
      });
      if (!response.ok) throw new Error('删除模型条目失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.admin.models });
    },
  });
}

/* ---------------- 系统设置（FR-13） ---------------- */

export function useAdminSettings(enabled = true) {
  return useQuery({
    queryKey: qk.admin.settings,
    queryFn: async (): Promise<SettingsView> => {
      const { data, response } = await api.GET('/admin/settings', {});
      if (!response.ok || !data) throw new Error('设置加载失败');
      return data;
    },
    staleTime: 15_000,
    enabled,
  });
}

/** 更新设置（记审计 setting.update；至少提供一项否则 422）。 */
export function usePatchSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: PatchSettingsReq): Promise<void> => {
      const { response } = await patchAdminSettings(body);
      if (!response.ok) throw new Error('保存设置失败');
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.admin.settings });
      // 设置变更记审计：审计查询页需要能筛出最新记录
      void qc.invalidateQueries({ queryKey: qk.admin.audit });
    },
  });
}

/* ---------------- 审计查询（FR-15） ---------------- */

export interface AuditLogsFilters {
  user_id: number | null;
  action: string;
  object_type: string;
  /** RFC3339 UTC（已由调用方从 datetime-local 转换） */
  from: string | null;
  /** RFC3339 UTC 排他边界 */
  to: string | null;
  page: number;
}

export interface AuditLogsPage {
  items: AuditRow[];
  total: number;
  page: number;
  page_size: number;
}

export function useAuditLogs(filters: AuditLogsFilters, enabled = true) {
  return useQuery({
    queryKey: [...qk.admin.audit, filters],
    queryFn: async (): Promise<AuditLogsPage> => {
      const { data, response } = await api.GET('/admin/audit-logs', {
        params: {
          query: {
            user_id: filters.user_id ?? undefined,
            action: filters.action || undefined,
            object_type: filters.object_type || undefined,
            from: filters.from ?? undefined,
            to: filters.to ?? undefined,
            page: filters.page,
          },
        },
      });
      if (!response.ok) throw new Error('审计日志加载失败');
      return {
        items: data?.items ?? [],
        total: data?.total ?? 0,
        page: data?.page ?? 1,
        page_size: data?.page_size ?? 20,
      };
    },
    staleTime: 15_000,
    placeholderData: keepPreviousData,
    enabled,
  });
}

/* ---------------- 反馈列表（FR-14 管理侧） ---------------- */

export interface FeedbacksFilters {
  kb_id: number | null;
  /** 1-4；null=全部 */
  reason: number | null;
  from: string | null;
  to: string | null;
  page: number;
}

export interface FeedbacksPage {
  items: FeedbackAdminRow[];
  total: number;
  page: number;
  page_size: number;
}

export function useFeedbacks(filters: FeedbacksFilters, enabled = true) {
  return useQuery({
    queryKey: [...qk.admin.feedbacks, filters],
    queryFn: async (): Promise<FeedbacksPage> => {
      const { data, response } = await api.GET('/admin/feedbacks', {
        params: {
          query: {
            kb_id: filters.kb_id ?? undefined,
            reason: filters.reason ?? undefined,
            from: filters.from ?? undefined,
            to: filters.to ?? undefined,
            page: filters.page,
          },
        },
      });
      if (!response.ok) throw new Error('反馈列表加载失败');
      return {
        items: data?.items ?? [],
        total: data?.total ?? 0,
        page: data?.page ?? 1,
        page_size: data?.page_size ?? 20,
      };
    },
    staleTime: 15_000,
    placeholderData: keepPreviousData,
    enabled,
  });
}
