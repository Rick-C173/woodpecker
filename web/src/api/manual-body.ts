/**
 * swagger 注解缺 requestBody 的端点补充封装（登录/注册/改密/上传/网页接入）。
 * 后端注解未声明请求体参数（如 /auth/login 的 Accept json），codegen 后 requestBody 为 never，
 * 无法直接走类型化 body——本文件以受控 cast 补齐请求体并显式声明响应类型；
 * 路径与响应 schema 仍来自 schema.d.ts（勿散落 cast）。
 * 契约修复（后端补注解 + gen:api）后可整体删除本文件。
 */

import { api } from './client';
import type { components } from './schema';

type Schemas = components['schemas'];

/** POST 端点 init 的模糊类型（cast 专用，勿直接使用）。 */
type PostInit = Parameters<typeof api.POST>[1];

export interface LoginReq {
  username: string;
  password: string;
}

export interface RegisterReq {
  username: string;
  password: string;
}

export interface ChangePasswordReq {
  old_password: string;
  new_password: string;
}

export interface UrlIngestReq {
  url: string;
}

/** POST /admin/users 请求体（swagger 注解缺 requestBody）。 */
export interface CreateUserReq {
  username: string;
  /** 初始密码，缺省由后端随机生成（≥8 位，AUTH_006） */
  password?: string;
  is_admin?: boolean;
}

/** PATCH /admin/users/{id} 请求体（swagger 注解缺 requestBody）。 */
export interface PatchUserReq {
  /** 1=启用 2=禁用（禁用自己 → USER_002） */
  status?: number;
  /** 撤最后一个管理员 → USER_003 */
  is_admin?: boolean;
}

/** PATCH /admin/settings 请求体（swagger 注解缺 requestBody）。 */
export interface PatchSettingsReq {
  /** glob 数组覆盖自定义清单；空数组=清空自定义（回到内置默认清单） */
  exclusion_rules?: string[];
  /** 自助注册开关（FR-01） */
  self_register?: boolean;
}

/** PATCH /conversations/{c} 请求体（重命名，swagger 注解缺 requestBody）。 */
export interface RenameConversationReq {
  title: string;
}

/** POST /messages/{m}/feedback 请求体（点踩，swagger 注解缺 requestBody）。 */
export interface MessageFeedbackReq {
  /** 1=答案不相关 2=引用错误 3=内容过时 4=其他 */
  reason: number;
  /** 补充说明（reason=4 必填） */
  comment?: string;
}

/** POST /admin/model-entries 请求体（codegen 把 params 误生成为 Record<string,never>）。 */
export interface CreateModelEntryReq {
  service_id: number;
  /** chat|embedding|rerank */
  purpose: string;
  model_name: string;
  /** 模型参数对象（temperature/top_p/dimensions/top_n 等） */
  params?: Record<string, unknown>;
  /** true 直接设为该用途默认 */
  is_default?: boolean;
}

/** PATCH /admin/model-entries/{id} 请求体（同上 params codegen 误生成）。 */
export interface PatchModelEntryReq {
  is_default?: boolean;
  model_name?: string;
  params?: Record<string, unknown>;
  purpose?: string;
}

/** POST /admin/model-entries：创建条目。 */
export async function postModelEntry(
  body: CreateModelEntryReq,
): Promise<RawResult<Schemas['service.ModelEntryItem']>> {
  const res = await api.POST('/admin/model-entries', { body } as unknown as PostInit);
  return toTyped(res);
}

/** PATCH /admin/model-entries/{id}：修改条目 / 设默认（is_default:true）。 */
export async function patchModelEntry(
  id: number,
  body: PatchModelEntryReq,
): Promise<RawResult<Schemas['service.ModelEntryItem']>> {
  const res = await api.PATCH('/admin/model-entries/{id}', {
    params: { path: { id } },
    body,
  } as unknown as PostInit);
  return toTyped(res);
}

/** 上传接口 rejected 条目（后端 gin.H{code,message}）。 */
export interface UploadRejected {
  code: string;
  message: string;
}

export interface UploadResult {
  items?: Schemas['service.DocumentItem'][];
  rejected?: UploadRejected[];
}

/** openapi-fetch 返回的最小形态（data 形态由本文件显式声明）。 */
interface RawResult<T> {
  data?: T;
  response: Response;
}

function toTyped<T>(res: { data?: unknown; response: Response }): RawResult<T> {
  return { data: res.data as T | undefined, response: res.response };
}

/** POST /auth/login（公开端点：AUTH_001/002 不触发全局登出，见 client.ts）。 */
export async function postLogin(
  body: LoginReq,
): Promise<RawResult<Schemas['service.AuthResult']>> {
  const res = await api.POST('/auth/login', { body } as unknown as PostInit);
  return toTyped(res);
}

/** POST /auth/register（公开端点）。 */
export async function postRegister(
  body: RegisterReq,
): Promise<RawResult<Schemas['service.AuthResult']>> {
  const res = await api.POST('/auth/register', { body } as unknown as PostInit);
  return toTyped(res);
}

/** POST /auth/password：成功后 token_version+1，旧 token 全失效，需重新登录。 */
export async function postChangePassword(
  body: ChangePasswordReq,
): Promise<RawResult<Schemas['service.AuthResult']>> {
  const res = await api.POST('/auth/password', { body } as unknown as PostInit);
  return toTyped(res);
}

/** POST /kbs/{kb}/documents/url：接入网页。 */
export async function postUrlIngest(
  kbId: number,
  body: UrlIngestReq,
): Promise<RawResult<Schemas['service.DocumentItem']>> {
  const res = await api.POST('/kbs/{kb}/documents/url', {
    params: { path: { kb: kbId } },
    body,
  } as unknown as PostInit);
  return toTyped(res);
}

/** POST /kbs/{kb}/documents/upload：multipart files[]，≤50 个/单文件 ≤5MB。 */
export async function postUploadFiles(
  kbId: number,
  files: File[],
): Promise<RawResult<UploadResult>> {
  const form = new FormData();
  for (const f of files) form.append('files[]', f, f.name);
  const res = await api.POST('/kbs/{kb}/documents/upload', {
    params: { path: { kb: kbId } },
    body: form,
    // FormData 让浏览器自动带 multipart boundary，禁止手工设置 Content-Type
  } as unknown as PostInit);
  return toTyped<UploadResult>(res);
}

/* ================= 管理端（F6） ================= */

/** POST /admin/users：创建用户，响应含一次性初始密码（仅此一次）。 */
export async function postCreateUser(
  body: CreateUserReq,
): Promise<RawResult<Schemas['handler.createUserResp']>> {
  const res = await api.POST('/admin/users', { body } as unknown as PostInit);
  return toTyped(res);
}

/** PATCH /admin/users/{id}：禁用/启用、设/撤管理员（USER_002/003 保护）。 */
export async function patchAdminUser(
  id: number,
  body: PatchUserReq,
): Promise<RawResult<Schemas['handler.patchUserResp']>> {
  const res = await api.PATCH('/admin/users/{id}', {
    params: { path: { id } },
    body,
  } as unknown as PostInit);
  return toTyped(res);
}

/** POST /admin/users/{id}/reset-password：重置密码，响应含一次性初始密码。 */
export async function postResetPassword(
  id: number,
): Promise<RawResult<Schemas['handler.resetPasswordResp']>> {
  const res = await api.POST('/admin/users/{id}/reset-password', {
    params: { path: { id } },
  });
  return toTyped(res);
}

/** PATCH /admin/settings：更新设置（记审计 setting.update）。 */
export async function patchAdminSettings(
  body: PatchSettingsReq,
): Promise<RawResult<{ message?: string }>> {
  const res = await api.PATCH('/admin/settings', { body } as unknown as PostInit);
  return toTyped<{ message?: string }>(res);
}

/* ================= 会话与反馈（F5） ================= */

/** PATCH /conversations/{c}：重命名会话。 */
export async function patchConversation(
  cId: number,
  body: RenameConversationReq,
): Promise<RawResult<{ message?: string }>> {
  const res = await api.PATCH('/conversations/{c}', {
    params: { path: { c: cId } },
    body,
  } as unknown as PostInit);
  return toTyped<{ message?: string }>(res);
}

/** POST /messages/{m}/feedback：点踩（重复提交覆盖）。 */
export async function postMessageFeedback(
  mId: number,
  body: MessageFeedbackReq,
): Promise<RawResult<{ message?: string }>> {
  const res = await api.POST('/messages/{m}/feedback', {
    params: { path: { m: mId } },
    body,
  } as unknown as PostInit);
  return toTyped<{ message?: string }>(res);
}
