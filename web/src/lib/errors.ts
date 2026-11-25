/**
 * 错误码 → 中文提示文案映射表（API设计.md §2，27 码全覆盖 + 兜底）。
 * 展示时优先查表；查不到时兜底展示后端 message + request_id。
 */

/** 统一错误响应体（{code, message, request_id}）。 */
export interface ApiErrorBody {
  code: string;
  message: string;
  request_id?: string | null;
}

/** 全局错误码表：AUTH_001~007 / USER_001~003 / KB_001 / DOC_001~004 / MODEL_001~004 /
 *  LLM_001~002 / SYS_001 / DOC_TASK_RUNNING / RATE_LIMITED / NOT_FOUND / VALIDATION_FAILED
 *  / INTERNAL（非 ServiceError 的 500 兜底，见 internal/httpx/response.go） */
export const ERROR_MESSAGES: Record<string, string> = {
  AUTH_001: '用户名或密码错误',
  AUTH_002: '账号已锁定，请 10 分钟后重试',
  AUTH_003: '账号已被禁用，请联系管理员',
  AUTH_004: '登录状态已失效，请重新登录',
  AUTH_005: '自助注册暂未开放，请联系管理员开通账号',
  AUTH_006: '密码长度至少 8 位',
  AUTH_007: '请先修改初始密码',
  USER_001: '用户名已存在，请换一个',
  USER_002: '不能禁用自己的账号',
  USER_003: '无法操作：至少需要保留一名管理员',
  KB_001: '知识库名称已存在',
  DOC_001: '仓库超过大小或文件数限制（≤1GB / 5 万文件）',
  DOC_002: '该文件格式不支持，请查看支持的格式清单',
  DOC_003: '仓库凭证无效或地址无法访问，请检查后重试',
  DOC_004: '同步失败：远端仓库暂时不可达，现有内容不受影响，可稍后重试',
  MODEL_001: '该用途尚未配置默认模型条目，请先在模型条目中设置默认',
  MODEL_002: '默认条目不可删除，请先将该用途的默认切换到其他条目',
  MODEL_003: '该服务下仍有模型条目，请先删除全部条目',
  MODEL_004: '该服务持有默认条目，不可停用；请先切换默认到其他服务',
  LLM_001: '模型服务暂时不可用，请稍后重新生成，或联系管理员检查模型配置',
  LLM_002: '模型服务返回异常，请联系管理员检查模型配置',
  SYS_001: '服务暂时不可用，请稍后重试',
  DOC_TASK_RUNNING: '该文档已有进行中的任务，请等待完成后再操作',
  RATE_LIMITED: '操作过于频繁，请稍后再试',
  NOT_FOUND: '内容不存在或已被删除',
  VALIDATION_FAILED: '提交内容有误，请检查表单',
  INTERNAL: '服务内部错误，请稍后重试',
};

/** 全部受支持的错误码（errors.ts 单测按此断言覆盖度）。 */
export const KNOWN_ERROR_CODES: readonly string[] = Object.keys(ERROR_MESSAGES);

/** 客户端可抛出的统一 API 错误类型（client.ts / sse.ts 归一化产物）。 */
export class ApiError extends Error {
  readonly code: string;
  readonly request_id: string | null;
  readonly status: number | null;

  constructor(body: Partial<ApiErrorBody>, status: number | null = null) {
    super(formatErrorMessage(body));
    this.name = 'ApiError';
    this.code = body.code ?? 'INTERNAL';
    this.request_id = body.request_id ?? null;
    this.status = status;
  }}

/**
 * 错误码 → 展示文案。已知码走映射表；未知码兜底展示后端 message；
 * 无 code 无 message（完全未知结构）归一化为 INTERNAL 文案。
 * 供 ErrorState / toast / 表单错误统一调用。
 */
export function formatErrorMessage(body: Partial<ApiErrorBody>): string {
  const code = body.code;
  if (code !== undefined && ERROR_MESSAGES[code] !== undefined) {
    return ERROR_MESSAGES[code];
  }
  if (body.message) {
    return body.message;
  }
  return code === undefined ? ERROR_MESSAGES['INTERNAL'] : '操作失败，请稍后重试';
}

/** request_id 展示串（报障截图用）；无则空串。 */
export function formatRequestId(requestId: string | null | undefined): string {
  return requestId ? `request_id: ${requestId}` : '';
}
