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
