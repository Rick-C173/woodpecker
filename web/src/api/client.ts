/**
 * API client（架构 §1.9 / §4.1）：
 * openapi-typescript 生成类型（src/api/schema.d.ts，勿手改）+ openapi-fetch 薄运行时。
 * 封装层职责：
 *   - 请求侧注入 Authorization: Bearer
 *   - 响应侧统一错误归一化：{code,message,request_id} → ApiError（errors.ts）
 *   - 401 全局处理：清 token → 广播 'logout'（路由层跳 /login?from=当前路径）
 *   - 403 AUTH_007：抛专用 ApiError 并广播 'change-password'（路由层跳 /change-password）
 * SSE 端点不走本 client（响应是事件流），提问走 src/lib/sse.ts。
 */

import createClient, { type Middleware } from 'openapi-fetch';
import type { paths } from './schema';
import { ApiError } from '@/lib/errors';
import { clearAuth, emitAuthEvent, getToken } from '@/lib/auth';

/** 公开端点：其 401 是业务结果（如 AUTH_001 密码错误、AUTH_010 SSO 兑换过期），不得触发全局登出跳转。 */
const PUBLIC_PATHS = ['/auth/login', '/auth/register', '/auth/oidc/status', '/auth/oidc/token'];

/** 解析后端统一错误信封 {code, message, request_id}。 */
async function parseErrorBody(
  response: Response,
): Promise<{ code: string; message: string; request_id: string | null }> {
  let body: { code?: string; message?: string; request_id?: string } = {};
  try {
    const text = await response.text();
    if (text) body = JSON.parse(text) as typeof body;
  } catch {
    /* 非 JSON 错误体走兜底 */
  }
  return {
    code: body.code ?? 'INTERNAL',
    message: body.message ?? '服务暂时不可用，请稍后重试',
    request_id: body.request_id ?? null,
  };
}

const authMiddleware: Middleware = {
  onRequest({ request }) {
    const token = getToken();
    if (token) {
      request.headers.set('Authorization', `Bearer ${token}`);
    }
    return request;
  },
  async onResponse({ request, response }) {
    if (response.ok) return response;
    const body = await parseErrorBody(response);

    // 401：token 过期 / 账号被禁用（AUTH_003）/ token_version 不符（AUTH_004）——被动踢出。
    // 公开端点的 401（AUTH_001/002）是业务结果，不触发全局登出。
    const isPublicPath = PUBLIC_PATHS.some((p) => request.url.includes(p));
    if (response.status === 401 && !isPublicPath) {
      clearAuth();
      emitAuthEvent('logout');
    } else if (body.code === 'AUTH_007') {
      // 首登未改密的兜底：守卫与登录响应标记不一致时，由响应全局跳改密页（双保险）。
      emitAuthEvent('change-password');
    }

    throw new ApiError(body, response.status);
  },
};

/** 全局唯一 API client 实例。 */
export const api = createClient<paths>({ baseUrl: '/api/v1' });
api.use(authMiddleware);

export type ApiClient = typeof api;
